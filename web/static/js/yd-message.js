/* yd 消息页胶水层：消费 WebSocket 事件、局部刷新线程/会话/好友面板，并维护通知与附件标签。 | yd message-page glue: consumes WebSocket events, refreshes thread/conversations/friends in place, and maintains notices and attachment chips. */
(function () {
  "use strict";

  var yd = window.yd || {};
  window.yd = yd;

  /* 去抖间隔：一次消息写入通常伴随 badge、notice 与 JSON 事件，刷新只需发生一次。 | Debounce interval: one write usually produces a badge, a notice and a JSON event, and the refresh only needs to happen once. */
  var REFRESH_DELAY = 180;

  var timers = {thread: null, conversations: null, friends: null};
  var refreshing = {thread: false, conversations: false};
  /* lastConversation 记录上一次交换结束时会话 ID，用于只在切换到另一个会话或发送后滚到底部。 |
     lastConversation remembers the conversation id at the previous swap, so the history scrolls to the bottom only when
     another conversation is opened or after a send. */
  var lastConversation = "";
  /* sending 在撰写区提交时置位；交换完成后据此把新消息滚入视野。 | sending is set when the composer submits, so the swap
     can bring the new message into view. */
  var sending = false;

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
  function refreshThread(conversationID) {
    if (!conversationID || !window.htmx || refreshing.thread) {
      return;
    }
    if (activeConversation() !== conversationID) {
      return;
    }
    refreshing.thread = true;
    var keepAtBottom = nearBottom();
    var done = function () {
      window.setTimeout(function () {
        refreshing.thread = false;
      }, 400);
    };
    var finish = function () {
      if (keepAtBottom) {
        scrollThread();
      }
      done();
    };
    try {
      var result = window.htmx.ajax("GET", "/messages/" + encodeURIComponent(conversationID) + "/history", {
        target: "#yd-message-thread-body",
        swap: "innerMorph"
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
    if (!document.getElementById("yd-conversation-list") || !window.htmx || refreshing.conversations) {
      return;
    }
    refreshing.conversations = true;
    var done = function () {
      window.setTimeout(function () {
        refreshing.conversations = false;
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

  /* onRealtime 把一条服务端事件分派到对应的局部刷新。 | onRealtime dispatches one server event to the matching partial refresh. */
  function onRealtime(event) {
    var payload = event && event.detail;
    if (!payload || !payload.yd_event) {
      return;
    }
    var conversationID = payload.conversation_id || "";
    var kind = payload.yd_event;

    if (kind.indexOf("message.") === 0 || kind === "conversation.changed") {
      if (conversationID && activeConversation() === conversationID) {
        debounce("thread", function () {
          refreshThread(conversationID);
        });
      }
      debounce("conversations", refreshConversations);
    }
    if (kind.indexOf("friend.") === 0) {
      debounce("friends", refreshFriends);
      debounce("conversations", refreshConversations);
    }
  }

  /* onSwap 在每次 htmx 交换后恢复滚动与编辑器；历史刷新只换消息区，撰写区因此不受影响。 |
     onSwap restores scrolling and the editor after every htmx swap; a history refresh swaps only the message region, so
     the composer is untouched. */
  function onSwap() {
    var id = activeConversation();
    if (id && (id !== lastConversation || sending)) {
      scrollThread();
    }
    sending = false;
    lastConversation = id;
    if (yd.messageEditor) {
      yd.messageEditor.init(document);
    }
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
