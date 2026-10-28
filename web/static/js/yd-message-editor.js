/* yd 消息编辑器初始化：把抽屉里的 textarea 升级为 EasyMDE Markdown 编辑器。 | yd message editor: upgrades the drawer's textarea to the EasyMDE Markdown editor. */
/* 本文件随框架静态资产合并进生成项目，因此所有消息页引用同一路径与同一份初始化逻辑。 | This file merges into generated projects through the framework static assets, so every message page shares one path and one initialiser. */
(function () {
  "use strict";

  var yd = window.yd || {};
  window.yd = yd;

  /* 就绪标记：同一个元素只升级一次，htmx 的重复 afterSwap 因此是幂等的。 | Ready flag: one element is upgraded once, so repeated htmx afterSwap calls are idempotent. */
  var READY = "ydEditorReady";

  /* 工具栏文案：编辑器自带的按钮标题是英文，这里按页面语言提供中英两份。 | Toolbar copy: the editor's built-in titles are English, so this small table supplies both languages. */
  var LABELS = {
    "zh-CN": {
      bold: "加粗",
      italic: "斜体",
      heading: "标题",
      strikethrough: "删除线",
      quote: "引用",
      unorderedList: "无序列表",
      orderedList: "有序列表",
      link: "链接",
      image: "图片",
      code: "行内代码",
      table: "表格",
      horizontalRule: "分隔线",
      preview: "预览",
      sideBySide: "并排预览",
      fullscreen: "全屏",
      undo: "撤销",
      redo: "重做"
    },
    "en-US": {
      bold: "Bold",
      italic: "Italic",
      heading: "Heading",
      strikethrough: "Strikethrough",
      quote: "Quote",
      unorderedList: "Unordered list",
      orderedList: "Ordered list",
      link: "Link",
      image: "Image",
      code: "Inline code",
      table: "Table",
      horizontalRule: "Horizontal rule",
      preview: "Preview",
      sideBySide: "Side by side",
      fullscreen: "Full screen",
      undo: "Undo",
      redo: "Redo"
    }
  };

  /* currentLang 把页面语言归一为两份工具栏文案之一。 | currentLang normalises the page language to one of the two toolbar tables. */
  function currentLang(element) {
    var value = element.getAttribute("data-lang") || document.documentElement.lang || "en";
    return value.toLowerCase().indexOf("zh") === 0 ? "zh-CN" : "en-US";
  }

  /* toolbar 使用 EasyMDE 的静态动作函数构造按钮，标题来自上面的表。compact 用于会话底部的内联撰写区：
     保留最常用的 Markdown 动作，把图片、表格与并排预览留给完整的抽屉编辑器，工具栏因此不会挤占聊天窗口。
     toolbar builds buttons from EasyMDE's static actions, with titles from the table above. compact is for the inline
     composer at the bottom of a conversation: it keeps the common Markdown actions and leaves images, tables and
     side-by-side preview to the full drawer editor, so the toolbar never crowds the chat window. */
  function toolbar(labels, compact) {
    var M = window.EasyMDE;
    if (!M) {
      return undefined;
    }
    var items = [
      {name: "bold", action: M.toggleBold, className: "yd-editor-tool", title: labels.bold},
      {name: "italic", action: M.toggleItalic, className: "yd-editor-tool", title: labels.italic},
      {name: "heading", action: M.toggleHeadingSmaller, className: "yd-editor-tool", title: labels.heading},
      {name: "strikethrough", action: M.toggleStrikethrough, className: "yd-editor-tool", title: labels.strikethrough},
      "|",
      {name: "quote", action: M.toggleBlockquote, className: "yd-editor-tool", title: labels.quote},
      {name: "unordered-list", action: M.toggleUnorderedList, className: "yd-editor-tool", title: labels.unorderedList},
      {name: "ordered-list", action: M.toggleOrderedList, className: "yd-editor-tool", title: labels.orderedList},
      "|",
      {name: "link", action: M.drawLink, className: "yd-editor-tool", title: labels.link}
    ];
    if (!compact) {
      items.push({name: "image", action: M.drawImage, className: "yd-editor-tool", title: labels.image});
    }
    items.push({name: "code", action: M.toggleCodeBlock, className: "yd-editor-tool", title: labels.code});
    if (!compact) {
      items.push(
        {name: "table", action: M.drawTable, className: "yd-editor-tool", title: labels.table},
        {name: "horizontal-rule", action: M.drawHorizontalRule, className: "yd-editor-tool", title: labels.horizontalRule}
      );
    }
    items.push(
      "|",
      {name: "preview", action: M.togglePreview, className: "yd-editor-tool no-disable", title: labels.preview}
    );
    if (!compact) {
      items.push({name: "side-by-side", action: M.toggleSideBySide, className: "yd-editor-tool no-disable no-mobile", title: labels.sideBySide});
    }
    items.push(
      {name: "fullscreen", action: M.toggleFullScreen, className: "yd-editor-tool no-disable no-mobile", title: labels.fullscreen},
      "|",
      {name: "undo", action: M.undo, className: "yd-editor-tool", title: labels.undo},
      {name: "redo", action: M.redo, className: "yd-editor-tool", title: labels.redo}
    );
    return items;
  }

  /* prefersDark 解析三态主题中的「跟随系统」。 | prefersDark resolves "system" in the three-state theme. */
  function prefersDark() {
    var theme = document.documentElement.getAttribute("data-theme") || "auto";
    if (theme === "dark") {
      return true;
    }
    if (theme === "light") {
      return false;
    }
    return !!(window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
  }

  /* applyTheme 只切换容器类，具体颜色由 components.css 的语义令牌给出。 | applyTheme toggles a container class only; components.css supplies the concrete colours from semantic tokens. */
  function applyTheme(element) {
    if (prefersDark()) {
      element.classList.add("yd-editor--dark");
    } else {
      element.classList.remove("yd-editor--dark");
    }
  }

  /* EasyMDE 约 320 KB，是消息页最大的一件资产，而多数访问只阅读。包装元素经 data-yd-editor-vendor
     声明同源地址，首次聚焦或抽屉打开时才加载；这段时间文本域仍可输入，升级后 EasyMDE 读取同一份内容。
     EasyMDE is about 320 KB, the message page's largest asset, while most visits only read. The wrapper declares
     its same-origin URL through data-yd-editor-vendor and the script loads on first focus or when a drawer opens;
     the textarea stays editable meanwhile, and EasyMDE adopts the same content on upgrade. */
  var vendorState = 0; // 0 idle, 1 loading, 2 settled (loaded or failed) | 空闲 / 加载中 / 已结算
  var vendorWaiters = [];

  /* vendorURL 从包装元素向上查找声明的地址。 | vendorURL walks up from the wrapper to the declared URL. */
  function vendorURL(element) {
    var scope = element;
    while (scope && scope.nodeType === 1) {
      var url = scope.getAttribute ? scope.getAttribute("data-yd-editor-vendor") : "";
      if (url) {
        return url;
      }
      scope = scope.parentElement;
    }
    return "";
  }

  /* flushVendor 在加载结算后运行所有等待者，并按当前脚本状态重新初始化页面。 |
     flushVendor runs every waiter once the load settles and then re-initialises the page against the loaded script. */
  function flushVendor() {
    var waiters = vendorWaiters;
    vendorWaiters = [];
    for (var i = 0; i < waiters.length; i++) {
      waiters[i]();
    }
    init(document);
  }

  /* loadVendor 幂等加载同源脚本：并发意图共享同一次请求。 | loadVendor loads the same-origin script idempotently:
     concurrent intents share one request. */
  function loadVendor(url, done) {
    if (!url) {
      return;
    }
    if (typeof window.EasyMDE === "function" || typeof window.EasyMDE === "object") {
      done();
      return;
    }
    if (vendorState === 1) {
      vendorWaiters.push(done);
      return;
    }
    if (vendorState === 2) {
      return;
    }
    vendorState = 1;
    vendorWaiters.push(done);
    var script = document.createElement("script");
    script.src = url;
    script.async = false;
    script.onload = function () {
      vendorState = 2;
      flushVendor();
    };
    script.onerror = function () {
      vendorState = 2;
      flushVendor();
    };
    document.head.appendChild(script);
  }

  /* eagerEditor 报告包装是否位于抽屉中：打开抽屉就是明确的撰写意图，可以立即加载编辑器。 |
     eagerEditor reports whether the wrapper sits in the drawer: opening it is an explicit intent to compose, so the
     editor loads at once. */
  function eagerEditor(element) {
    return !!(element && element.closest && element.closest("#yd-drawer-body"));
  }

  /* initOne 升级一个编辑器；缺少脚本或 textarea 时保留回退输入框。 | initOne upgrades one editor; a missing script or textarea keeps the fallback input usable. */
  function initOne(element) {
    if (!element || element.getAttribute(READY) === "1") {
      return;
    }
    var textarea = element.querySelector("textarea");
    if (!textarea) {
      return;
    }
    if (typeof window.EasyMDE !== "function" && typeof window.EasyMDE !== "object") {
      // The compact inline composer used to upgrade only after the user focused its textarea; that changed the
      // composer's height under the pointer and made the first click feel jumpy. A compact editor is the primary
      // input on the message page, so load it as soon as the composer exists. Full drawer editors remain eager on
      // open and every other editor stays lazy. | 会话底部的紧凑编辑器过去只在用户聚焦 textarea 后才升级，插入
      // 点下方的撰写区会因此跳动，第一次点击显得不自然。紧凑编辑器是消息页的主输入区，因此在它出现时就加载；
      // 抽屉编辑器仍在打开时立即加载，其它编辑器继续惰性加载。
      if (eagerEditor(element) || element.hasAttribute("data-yd-editor-compact")) {
        loadVendor(vendorURL(element), function () {
          initOne(element);
        });
      }
      return;
    }

    var labels = LABELS[currentLang(element)];
    var compact = element.hasAttribute("data-yd-editor-compact");
    var editor;
    try {
      editor = new window.EasyMDE({
        element: textarea,
        /* Never let EasyMDE fetch its legacy Font Awesome stylesheet from a CDN; the toolbar
           below is decorated with Lucide SVGs from the page's own sprite. | 绝不让 EasyMDE 从 CDN
           拉取遗留的 Font Awesome 样式表；下面的工具栏使用页面自带 sprite 中的 Lucide SVG。 */
        autoDownloadFontAwesome: false,
        spellChecker: false,
        nativeSpellcheck: false,
        status: false,
        autofocus: false,
        forceSync: true,
        minHeight: compact ? "96px" : "320px",
        maxHeight: compact ? "180px" : undefined,
        placeholder: textarea.getAttribute("placeholder") || "",
        toolbar: toolbar(labels, compact),
        renderingConfig: {
          singleLineBreaks: false
        }
      });
    } catch (error) {
      /* 初始化失败时保持 textarea 可见：用户仍可写 Markdown，只是没有工具栏。 | On failure the textarea stays visible: the user can still write Markdown, just without the toolbar. */
      element.removeAttribute(READY);
      return;
    }

    element.setAttribute(READY, "1");
    element.__ydEditor = editor;
    /* EasyMDE 的工具栏由脚本在运行时构造；图标统一从隐藏调色板克隆 Lucide SVG，不依赖图标字体。 |
       EasyMDE builds its toolbar at runtime; every icon is cloned from the hidden palette as a Lucide
       SVG, so no icon font is involved. */
    if (window.ydUI && typeof window.ydUI.decorateEditorToolbar === "function") {
      window.ydUI.decorateEditorToolbar(editor, element);
    }
    applyTheme(element);
  }

  /* init 扫描文档树；重复调用是幂等的，因此 htmx 每次交换后都可安全调用。 | init scans the document; repeated calls are idempotent, so every htmx swap may call it safely. */
  function init(root) {
    var scope = root && root.querySelectorAll ? root : document;
    var elements = scope.querySelectorAll("[data-yd-editor]");
    for (var i = 0; i < elements.length; i++) {
      initOne(elements[i]);
    }
  }

  /* destroy 把编辑器还原为 textarea；显式清理时使用，htmx 交换后旧节点会自然回收。 | destroy restores the textarea; it is for explicit cleanup, while htmx swaps reclaim old nodes naturally. */
  function destroy(element) {
    if (element && element.__ydEditor && typeof element.__ydEditor.toTextArea === "function") {
      element.__ydEditor.toTextArea();
      element.__ydEditor = null;
    }
  }

  yd.messageEditor = {init: init, destroy: destroy};

  /* 首次聚焦或点按编辑器时再加载 EasyMDE：阅读消息的访问不为撰写付费。 |
     EasyMDE loads on first focus or pointer press: a read-only visit does not pay for the composer. */
  ["focusin", "pointerdown"].forEach(function (name) {
    document.addEventListener(name, function (event) {
      var target = event.target;
      var wrapper = target && target.closest ? target.closest("[data-yd-editor]") : null;
      if (!wrapper || wrapper.getAttribute(READY) === "1") {
        return;
      }
      if (typeof window.EasyMDE === "function" || typeof window.EasyMDE === "object") {
        initOne(wrapper);
        return;
      }
      loadVendor(vendorURL(wrapper), function () {
        initOne(wrapper);
      });
    }, true);
  });

  document.addEventListener("DOMContentLoaded", function () {
    init(document);
  });
  /* 交换后只检查被替换的子树：实时事件推来的角标片段不包含编辑器，扫描整个文档对它们是纯开销。 |
     Only the swapped subtree is checked after a swap: the badge fragments pushed by realtime events contain no
     editor, and scanning the whole document for them is pure overhead. */
  ["htmx:after:swap", "htmx:afterSwap"].forEach(function (name) {
    document.addEventListener(name, function (event) {
      var detail = (event && event.detail) || {};
      var ctx = detail.ctx || {};
      var target = ctx.target || detail.target;
      if (!target || target.nodeType !== 1) {
        init(document);
        return;
      }
      if (target.matches && target.matches("[data-yd-editor]")) {
        initOne(target);
        return;
      }
      if (target.querySelector && target.querySelector("[data-yd-editor]")) {
        init(target);
      }
    });
  });

  /* 主题变化：属性改写与系统偏好变化都要同步到已存在的编辑器。 | Theme changes: both an attribute rewrite and a system preference change reach existing editors. */
  if (window.MutationObserver) {
    var observer = new MutationObserver(function () {
      var elements = document.querySelectorAll("[data-yd-editor]");
      for (var i = 0; i < elements.length; i++) {
        applyTheme(elements[i]);
      }
    });
    observer.observe(document.documentElement, {attributes: true, attributeFilter: ["data-theme"]});
  }
  if (window.matchMedia) {
    var media = window.matchMedia("(prefers-color-scheme: dark)");
    if (typeof media.addEventListener === "function") {
      media.addEventListener("change", function () {
        var elements = document.querySelectorAll("[data-yd-editor]");
        for (var i = 0; i < elements.length; i++) {
          applyTheme(elements[i]);
        }
      });
    }
  }

  /* 脚本以 defer 加载，DOM 已就绪；立即初始化一次。 | The script loads with defer, so the DOM is ready; initialise once now. */
  init(document);
})();
