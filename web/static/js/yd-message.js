/* yd 消息页胶水层：消费 WebSocket 事件、局部刷新线程/会话/好友面板，并维护通知与附件标签。 | yd message-page glue: consumes WebSocket events, refreshes thread/conversations/friends in place, and maintains notices and attachment chips. */
(function () {
  "use strict";

  var yd = window.yd || {};
  window.yd = yd;

  /* 去抖间隔：一次消息写入通常伴随 badge、notice 与 JSON 事件，刷新只需发生一次。 | Debounce interval: one write usually produces a badge, a notice and a JSON event, and the refresh only needs to happen once. */
  var REFRESH_DELAY = 180;

  var timers = {thread: null, conversations: null, friends: null};
  var refreshing = {thread: false, conversations: false};
  /* pending 记录刷新锁被占用期间到达的最新一次请求：锁释放后补跑一次，事件不会因为落在 400ms
     冷却窗口里而丢失。 | pending remembers the newest request that arrived while a refresh lock was held:
     it runs once after the lock releases, so an event inside the 400ms cooldown is never lost. */
  var pending = {thread: "", threadDelta: false, conversations: false};
  /* readTimer 合并群聊中短时间内的多条已读回执：回执只改变自己消息上的读数，没有必要为每个人各重绘一次。 |
     readTimer coalesces the read receipts a group produces within a short window: a receipt only changes the
     counts on the user's own messages, so one redraw per member is unnecessary. */
  var readTimer = null;
  /* lastConversation 记录上一次交换结束时会话 ID，用于只在切换到另一个会话或发送后滚到底部。 |
     lastConversation remembers the conversation id at the previous swap, so the history scrolls to the bottom only when
     another conversation is opened or after a send. */
  var lastConversation = "";
  /* sending 在撰写区提交时置位；交换完成后据此把新消息滚入视野。 | sending is set when the composer submits, so the swap
     can bring the new message into view. */
  var sending = false;
  /* lastSendAt 记录本标签页最近一次发送时刻：发送的 POST 响应已经渲染了新消息，随后回传的自身事件
     若再触发一次历史刷新就是纯重复。 | lastSendAt records this tab's latest send: the POST response already
     rendered the new message, so a self event arriving right after must not trigger a second history fetch. */
  var lastSendAt = 0;
  /* localUserID 缓存 body 上的当前用户 ID，用于区分「自己的事件」与「同账号其它标签页的事件」。 |
     localUserID caches the current user id from body, distinguishing this user's events from this tab's own send. */
  var localUserID = null;

  function currentUserID() {
    if (localUserID === null) {
      localUserID = document.body ? (document.body.getAttribute("data-yd-user-id") || "") : "";
    }
    return localUserID;
  }

  /* hasOwnMessages 报告当前线程是否含自己的消息：读回执只渲染在这些消息上，别人的已读不影响其它气泡。 |
     hasOwnMessages reports whether the open thread contains the user's own messages: read receipts render only on
     those, so a peer's read never changes any other bubble. */
  function hasOwnMessages() {
    var body = threadBody();
    return !!(body && body.querySelector(".yd-msg--mine"));
  }

  /* debounce 按槽位去抖，避免线程与会话列表互相阻塞。 | debounce de-bounces per slot, so the thread and the conversation list never block each other. */
  function debounce(slot, fn) {
    if (timers[slot]) {
      window.clearTimeout(timers[slot]);
    }
    timers[slot] = window.setTimeout(function () {
      timers[slot] = null;
      fn();
    }, REFRESH_DELAY);
  }

  /* activeConversation 返回当前线程的会话 ID，没有打开线程时为空。 | activeConversation returns the open thread's conversation id, or empty when no thread is open. */
  function activeConversation() {
    var thread = document.getElementById("yd-message-thread");
    return thread ? (thread.getAttribute("data-conversation") || "") : "";
  }

  /* threadBody 返回历史消息的滚动容器。 | threadBody returns the scrolling history container. */
  function threadBody() {
    return document.getElementById("yd-message-thread-body");
  }

  /* nearBottom 报告历史区是否已接近底部：只有此时新消息到达才自动滚动，避免打断正在翻旧消息的读者。 |
     nearBottom reports whether the history is close to the bottom: only then does an arrival scroll automatically, so a
     reader browsing older messages is not interrupted. */
  function nearBottom() {
    var body = threadBody();
    if (!body) {
      return true;
    }
    return body.scrollHeight - body.scrollTop - body.clientHeight < 120;
  }

  /* scrollThread 把线程滚到底部：新消息到达后用户看到的是最新内容。 | scrollThread moves the thread to the bottom so the user sees the newest content after an arrival. */
  function scrollThread() {
    var body = threadBody();
    if (body) {
      body.scrollTop = body.scrollHeight;
    }
  }

  /* refreshThread 重取当前会话的历史区；撰写区不参与交换，因此草稿与编辑器实例都会保留。 |
     refreshThread refetches the open conversation's history region; the composer is not swapped, so its draft and editor
     instance survive. */
  /* lastMessageID 返回历史区最后一条消息的 ID，作为增量追加的锚点。 |
     lastMessageID returns the id of the last rendered message, the anchor of an incremental append. */
  function lastMessageID() {
    var body = threadBody();
    if (!body || !body.querySelectorAll) {
      return "";
    }
    var nodes = body.querySelectorAll("[data-yd-message-id]");
    if (!nodes.length) {
      return "";
    }
    return nodes[nodes.length - 1].getAttribute("data-yd-message-id") || "";
  }

  /* refreshThread 重取当前会话的历史区；delta 为真时只请求锚点之后的新消息并以 beforeend 追加，
     一条新消息因此不再重绘五十条卡片。编辑、撤回、已读回执与锚点缺失仍走整段 innerMorph。
     refreshThread refetches the open conversation's history; with delta it requests only messages after the
     anchor and appends them with beforeend, so one new message no longer redraws fifty cards. Edits, recalls,
     read receipts and a missing anchor still take the full innerMorph path. */
  function refreshThread(conversationID, delta) {
    if (!conversationID || !window.htmx) {
      return;
    }
    if (refreshing.thread) {
      // 队列里只要有一次「必须整段重取」的请求，补跑就不能退化成增量追加。 |
      // If any queued request needs a full refresh, the trailing run must not degrade to an append.
      pending.threadDelta = pending.thread === "" ? !!delta : (pending.threadDelta && !!delta);
      pending.thread = conversationID;
      return;
    }
    if (activeConversation() !== conversationID) {
      return;
    }
    var after = delta ? lastMessageID() : "";
    if (!after) {
      delta = false;
    }
    refreshing.thread = true;
    var keepAtBottom = nearBottom();
    var done = function () {
      window.setTimeout(function () {
        refreshing.thread = false;
        var queued = pending.thread;
        var queuedDelta = pending.threadDelta;
        pending.thread = "";
        pending.threadDelta = false;
        if (queued) {
          refreshThread(queued, queuedDelta);
        }
      }, 400);
    };
    var finish = function () {
      if (keepAtBottom) {
        scrollThread();
      }
      done();
    };
    var url = "/messages/" + encodeURIComponent(conversationID) + "/history";
    if (delta) {
      url += "?after=" + encodeURIComponent(after);
    }
    try {
      var result = window.htmx.ajax("GET", url, {
        target: "#yd-message-thread-body",
        swap: delta ? "beforeend" : "innerMorph"
      });
      if (result && typeof result.then === "function") {
        result.then(finish, done);
      } else {
        finish();
      }
    } catch (error) {
      done();
    }
  }

  /* refreshConversations 重取会话列表；活动会话 ID 用于保留高亮。 | refreshConversations refetches the list and keeps the active conversation highlighted. */
  function refreshConversations() {
    if (!document.getElementById("yd-conversation-list") || !window.htmx) {
      return;
    }
    if (refreshing.conversations) {
      pending.conversations = true;
      return;
    }
    refreshing.conversations = true;
    var done = function () {
      window.setTimeout(function () {
        refreshing.conversations = false;
        if (pending.conversations) {
          pending.conversations = false;
          refreshConversations();
        }
      }, 400);
    };
    var query = activeConversation() ? "?active=" + encodeURIComponent(activeConversation()) : "";
    try {
      var result = window.htmx.ajax("GET", "/messages/conversations" + query, {
        target: "#yd-conversation-list",
        swap: "outerMorph"
      });
      if (result && typeof result.then === "function") {
        result.then(done, done);
      } else {
        done();
      }
    } catch (error) {
      done();
    }
  }

  /* refreshFriends 重取好友面板；其它页面上不存在该面板时是空操作。 | refreshFriends refetches the friend panel and is a no-op on pages that do not carry it. */
  function refreshFriends() {
    if (!document.getElementById("yd-friends-panel") || !window.htmx) {
      return;
    }
    try {
      window.htmx.ajax("GET", "/friends/summary", {
        target: "#yd-friends-panel",
        swap: "outerMorph"
      });
    } catch (error) {
      /* 一次刷新失败由下一次事件重试；这里不弹错误，避免把网络抖动放大成噪音。 | A failed refresh is retried by the next event; no error is shown, so a network blip does not become noise. */
    }
  }

  /* refreshFriendSearch 重取当前好友检索结果：好友申请被接受后，旧片段里的「添加」按钮必须立刻消失。
     只有输入框与结果容器同时存在且查询非空时才发请求，其它页面与空查询都是空操作。 |
     refreshFriendSearch refetches the active friend search: once a request is accepted, the stale "add" button must
     disappear at once. It only requests while both the input and the results container exist and the query is
     non-empty; other pages and an empty query are no-ops. */
  function refreshFriendSearch() {
    var input = document.getElementById("yd-friend-q");
    var target = document.getElementById("yd-friend-search-results");
    if (!input || !target || !window.htmx) {
      return;
    }
    var query = (input.value || "").trim();
    if (!query) {
      return;
    }
    try {
      window.htmx.ajax("GET", "/friends/search?q=" + encodeURIComponent(query), {
        target: "#yd-friend-search-results",
        swap: "innerMorph"
      });
    } catch (error) {
      /* 与好友面板一样，失败由下一次事件重试，不弹额外错误。 | As with the friend panel, the next event retries and no extra error is shown. */
    }
  }

  /* onRealtime 把一条服务端事件分派到对应的局部刷新。 | onRealtime dispatches one server event to the matching partial refresh. */
  function onRealtime(event) {
    var payload = event && event.detail;
    if (!payload || !payload.yd_event) {
      return;
    }
    var conversationID = payload.conversation_id || "";
    var kind = payload.yd_event;
    var actorID = payload.actor_id || "";
    var self = actorID !== "" && actorID === currentUserID();

    /* 已读事件不改变会话预览与顺序：自己的已读由徽标帧更新，别人的已读只影响我自己的消息上的
       回执。只有「对方读了、且当前线程确有我的消息」才需要重取历史；会话栏完全不动。
       过去每个已读事件都会让群内每个客户端重取整段历史与会话栏，N 人群一条新消息即可放大成
       N 次全量渲染。 |
       A read event never changes a preview or the rail order: the reader's own badge is pushed separately, and a
       peer's read only affects receipts on my own messages. Refetch history only when someone else read and the
       open thread really contains my messages; the rail never moves. Previously every read event made every group
       client refetch the whole history and rail, so one message in an N-member group multiplied into N full renders. */
    if (kind === "message.read") {
      if (!self && conversationID && activeConversation() === conversationID && hasOwnMessages()) {
        if (readTimer) {
          window.clearTimeout(readTimer);
        }
        readTimer = window.setTimeout(function () {
          readTimer = null;
          if (activeConversation() === conversationID && hasOwnMessages()) {
            refreshThread(conversationID, false);
          }
        }, 800);
      }
      return;
    }

    /* 本标签页刚发送的消息已经由 POST 响应渲染；回传的自身 created 事件无需再刷一次。其它标签页
       的 lastSendAt 为 0，仍会正常刷新。 |
       A message just sent from this tab was already rendered by the POST response, so the echoed self created
       event needs no second refresh. Other tabs keep lastSendAt at 0 and still refresh normally. */
    if (kind === "message.created" && self && Date.now() - lastSendAt < 2500) {
      return;
    }

    if (kind.indexOf("message.") === 0 || kind === "conversation.changed") {
      if (conversationID && activeConversation() === conversationID) {
        // 只有「新消息」可以增量追加；编辑、撤回、置顶与已读回执都可能改变已渲染的卡片。 |
        // Only a new message can be appended; edits, recalls, pins and read receipts may change cards already
        // on screen.
        var appendable = kind === "message.created";
        debounce("thread", function () {
          refreshThread(conversationID, appendable);
        });
      }
      debounce("conversations", refreshConversations);
    }
    if (kind.indexOf("friend.") === 0) {
      debounce("friends", function () {
        refreshFriends();
        refreshFriendSearch();
      });
      debounce("conversations", refreshConversations);
    }
  }

  /* onSwap 在每次 htmx 交换后恢复滚动与编辑器；历史刷新只换消息区，撰写区因此不受影响。 |
     onSwap restores scrolling and the editor after every htmx swap; a history refresh swaps only the message region, so
     the composer is untouched. */
  /* quietSwap 报告一次交换是否只替换了角标或通知。实时事件一次会推四个角标片段，每个片段都会触发
     afterSwap；在这些交换上重扫编辑器与联系人列表纯属浪费，页面其它部分也没有变化。 |
     quietSwap reports whether a swap only replaced a badge or a notice. One realtime event pushes four badge
     fragments and each fires afterSwap; rescanning editors and contacts there is pure waste, and nothing else
     on the page changed. */
  function quietSwap(event) {
    var detail = (event && event.detail) || {};
    var ctx = detail.ctx || {};
    var target = ctx.target || detail.target;
    if (!target || target.nodeType !== 1) {
      return false;
    }
    var id = target.id;
    return id === "yd-unread-badge" ||
      id === "yd-unread-menu-badge" ||
      id === "yd-sidebar-unread" ||
      id === "yd-sidebar-friend-requests" ||
      id === "yd-message-notices" ||
      id === "yd-connection";
  }

  function onSwap(event) {
    if (quietSwap(event)) {
      return;
    }
    var id = activeConversation();
    if (id && (id !== lastConversation || sending)) {
      scrollThread();
    }
    sending = false;
    lastConversation = id;
    /* 编辑器初始化由 yd-message-editor.js 自己的 afterSwap 监听器负责；这里再扫一遍整个文档是重复工作。 |
       The editor initialises itself from yd-message-editor.js's own afterSwap listener; scanning the whole document
       again here duplicates that work. */
    syncContactSelection();
    filterContacts();
  }

  /* 会话栏页签：在「会话」与「联系人」之间切换；只读角色没有联系人页签时是空操作。
     Conversation-rail tabs: switch between conversations and contacts; a no-op when a read-only role has no contacts
     tab. */
  function activateMsgTab(name) {
    var tabs = document.querySelectorAll("[data-yd-msg-tab]");
    for (var i = 0; i < tabs.length; i++) {
      var selected = tabs[i].getAttribute("data-yd-msg-tab") === name;
      tabs[i].setAttribute("aria-selected", selected ? "true" : "false");
      tabs[i].setAttribute("tabindex", selected ? "0" : "-1");
    }
    var panels = document.querySelectorAll("[data-yd-msg-panel]");
    for (var j = 0; j < panels.length; j++) {
      panels[j].hidden = panels[j].getAttribute("data-yd-msg-panel") !== name;
    }
  }

  /* 联系人搜索只过滤已渲染的行，不发起请求；姓名与登录名都参与匹配。
     The contact search filters the rendered rows without a request; both display name and username take part. */
  function filterContacts() {
    var input = document.querySelector("[data-yd-contact-search]");
    if (!input) {
      return;
    }
    var query = (input.value || "").trim().toLowerCase();
    var rows = document.querySelectorAll("[data-yd-contact]");
    for (var i = 0; i < rows.length; i++) {
      var name = (rows[i].getAttribute("data-yd-contact-name") || "").toLowerCase();
      var username = (rows[i].getAttribute("data-yd-contact-username") || "").toLowerCase();
      rows[i].hidden = query !== "" && name.indexOf(query) === -1 && username.indexOf(query) === -1;
    }
  }

  /* 勾选数量驱动群名区与提交按钮：少于两位好友时两者保持隐藏/禁用，与服务端「至少两人建群」的入口一致。
     The checked count drives the group field and the submit button: with fewer than two friends both stay hidden or
     disabled, matching the server's "at least two friends make a group" entry point. */
  function syncContactSelection() {
    var checks = document.querySelectorAll("[data-yd-contact-check]");
    var count = 0;
    for (var i = 0; i < checks.length; i++) {
      if (checks[i].checked) {
        count++;
      }
    }
    var countEl = document.querySelector("[data-yd-contact-count]");
    if (countEl) {
      countEl.textContent = String(count);
    }
    var group = document.querySelector("[data-yd-contact-group]");
    if (group) {
      group.hidden = count < 2;
    }
    var hint = document.querySelector("[data-yd-contact-hint]");
    if (hint) {
      hint.hidden = count >= 2;
    }
    var submit = document.querySelector("[data-yd-contact-submit]");
    if (submit) {
      submit.disabled = count < 2;
    }
  }

  /* 撰写区提交成功后清空草稿：EasyMDE 的实例可能被 morph 复用，不清空会把刚发出去的内容留在输入框里。 |
     Clear the draft after a successful composer send: morph may reuse the EasyMDE instance, and without this the
     just-sent text would remain in the input. */
  function clearComposer() {
    var wrapper = document.querySelector("#yd-msg-composer [data-yd-editor]");
    var editor = wrapper && wrapper.__ydEditor;
    if (editor && typeof editor.value === "function") {
      editor.value("");
    }
    var title = document.getElementById("yd-msg-composer-title");
    if (title) {
      title.value = "";
    }
    var form = document.getElementById("yd-msg-composer-form");
    if (form) {
      var boxes = form.querySelectorAll("input[type='checkbox']");
      for (var i = 0; i < boxes.length; i++) {
        boxes[i].checked = false;
      }
      var type = form.querySelector("select[name='type_key']");
      var fallback = form.getAttribute("data-yd-default-type");
      if (type && fallback) {
        type.value = fallback;
      }
    }
    var attachments = document.getElementById("yd-msg-composer-attachments");
    if (attachments) {
      attachments.innerHTML = "";
    }
  }

  /* 撰写区提交时记下发送意图，交换完成后把最新一条消息滚入视野。 |
     Record the send intent when the composer submits, so the swap can bring the newest message into view. */
  ["htmx:before:request", "htmx:beforeRequest"].forEach(function (name) {
    document.addEventListener(name, function (event) {
      var detail = event.detail || {};
      var ctx = detail.ctx || {};
      var elt = ctx.sourceElement || detail.elt;
      if (elt && elt.id === "yd-msg-composer-form") {
        sending = true;
        lastSendAt = Date.now();
      }
    });
  });

  /* 发送成功（2xx）立即清空撰写区；失败时保留草稿与附件，用户可以直接修正后重试。 |
     A successful send (2xx) clears the composer at once; a failure keeps the draft and attachments so the user can fix
     and retry in place. */
  ["htmx:after:request", "htmx:afterRequest"].forEach(function (name) {
    document.addEventListener(name, function (event) {
      var detail = event.detail || {};
      var ctx = detail.ctx || {};
      var status = typeof ctx.status === "number" ? ctx.status : (ctx.response && ctx.response.status);
      if (typeof status !== "number") {
        status = detail.xhr && detail.xhr.status;
      }
      var elt = ctx.sourceElement || detail.elt;
      if (typeof status === "number" && status >= 200 && status < 300 && elt && elt.id === "yd-msg-composer-form") {
        clearComposer();
      }
    });
  });

  /* Ctrl/Cmd+Enter sends the inline composer. EasyMDE keeps plain Enter for a newline, so Markdown editing is never
     broken by a send shortcut. | Ctrl/Cmd+Enter 发送内联撰写区；EasyMDE 的普通回车仍用于换行，Markdown 编辑不会被发送快捷键破坏。 */
  document.addEventListener("keydown", function (event) {
    if (!(event.ctrlKey || event.metaKey) || event.key !== "Enter" || event.isComposing || event.defaultPrevented) {
      return;
    }
    var target = event.target;
    if (!target || !target.closest || !target.closest("#yd-msg-composer-form")) {
      return;
    }
    var form = document.getElementById("yd-msg-composer-form");
    if (!form) {
      return;
    }
    event.preventDefault();
    form.requestSubmit();
  }, true);

  /* 页签点击与「新建会话」入口：入口同时把焦点送进联系人搜索框，键盘用户不必再 Tab 一次。 |
     Tab clicks and the "new chat" entry: the entry also focuses the contact search so keyboard users need no extra
     Tab stop. */
  document.addEventListener("click", function (event) {
    var target = event.target;
    if (!target || !target.closest) {
      return;
    }
    var tab = target.closest("[data-yd-msg-tab]");
    if (tab) {
      event.preventDefault();
      activateMsgTab(tab.getAttribute("data-yd-msg-tab"));
      return;
    }
    var opener = target.closest("[data-yd-msg-tab-open]");
    if (opener) {
      event.preventDefault();
      activateMsgTab(opener.getAttribute("data-yd-msg-tab-open"));
      var search = document.querySelector("[data-yd-contact-search]");
      if (search) {
        search.focus();
      }
    }
  });

  /* 左右方向键在页签间移动，符合 WAI-ARIA 的 tabs 键盘模型。 |
     Left and right arrows move between tabs, following the WAI-ARIA tabs keyboard model. */
  document.addEventListener("keydown", function (event) {
    var target = event.target;
    if (!target || !target.closest) {
      return;
    }
    var tab = target.closest("[data-yd-msg-tab]");
    if (!tab || (event.key !== "ArrowLeft" && event.key !== "ArrowRight")) {
      return;
    }
    var tabs = document.querySelectorAll("[data-yd-msg-tab]");
    if (tabs.length < 2) {
      return;
    }
    var index = Array.prototype.indexOf.call(tabs, tab);
    var next = event.key === "ArrowRight" ? (index + 1) % tabs.length : (index - 1 + tabs.length) % tabs.length;
    event.preventDefault();
    activateMsgTab(tabs[next].getAttribute("data-yd-msg-tab"));
    tabs[next].focus();
  });

  document.addEventListener("input", function (event) {
    if (event.target && event.target.matches && event.target.matches("[data-yd-contact-search]")) {
      filterContacts();
    }
  });

  document.addEventListener("change", function (event) {
    if (event.target && event.target.matches && event.target.matches("[data-yd-contact-check]")) {
      syncContactSelection();
    }
  });

  document.addEventListener("yd:realtime", onRealtime);
  ["htmx:after:swap", "htmx:afterSwap"].forEach(function (name) {
    document.addEventListener(name, onSwap);
  });

  document.addEventListener("click", function (event) {
    var remove = event.target && event.target.closest ? event.target.closest("[data-yd-attachment-remove]") : null;
    if (!remove) {
      return;
    }
    var chip = remove.closest("[data-yd-attachment]");
    if (chip && chip.parentNode) {
      chip.parentNode.removeChild(chip);
    }
  });

  document.addEventListener("DOMContentLoaded", function () {
    scrollThread();
    lastConversation = activeConversation();
    syncContactSelection();
    filterContacts();
  });
  scrollThread();
  syncContactSelection();
  filterContacts();
})();
