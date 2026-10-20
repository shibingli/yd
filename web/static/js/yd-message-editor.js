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
      {name: "bold", action: M.toggleBold, className: "fa fa-bold", title: labels.bold},
      {name: "italic", action: M.toggleItalic, className: "fa fa-italic", title: labels.italic},
      {name: "heading", action: M.toggleHeadingSmaller, className: "fa fa-header", title: labels.heading},
      {name: "strikethrough", action: M.toggleStrikethrough, className: "fa fa-strikethrough", title: labels.strikethrough},
      "|",
      {name: "quote", action: M.toggleBlockquote, className: "fa fa-quote-left", title: labels.quote},
      {name: "unordered-list", action: M.toggleUnorderedList, className: "fa fa-list-ul", title: labels.unorderedList},
      {name: "ordered-list", action: M.toggleOrderedList, className: "fa fa-list-ol", title: labels.orderedList},
      "|",
      {name: "link", action: M.drawLink, className: "fa fa-link", title: labels.link}
    ];
    if (!compact) {
      items.push({name: "image", action: M.drawImage, className: "fa fa-image", title: labels.image});
    }
    items.push({name: "code", action: M.toggleCodeBlock, className: "fa fa-code", title: labels.code});
    if (!compact) {
      items.push(
        {name: "table", action: M.drawTable, className: "fa fa-table", title: labels.table},
        {name: "horizontal-rule", action: M.drawHorizontalRule, className: "fa fa-minus", title: labels.horizontalRule}
      );
    }
    items.push(
      "|",
      {name: "preview", action: M.togglePreview, className: "fa fa-eye no-disable", title: labels.preview}
    );
    if (!compact) {
      items.push({name: "side-by-side", action: M.toggleSideBySide, className: "fa fa-columns no-disable no-mobile", title: labels.sideBySide});
    }
    items.push(
      {name: "fullscreen", action: M.toggleFullScreen, className: "fa fa-arrows-alt no-disable no-mobile", title: labels.fullscreen},
      "|",
      {name: "undo", action: M.undo, className: "fa fa-undo", title: labels.undo},
      {name: "redo", action: M.redo, className: "fa fa-repeat", title: labels.redo}
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
      return;
    }

    var labels = LABELS[currentLang(element)];
    var compact = element.hasAttribute("data-yd-editor-compact");
    var editor;
    try {
      editor = new window.EasyMDE({
        element: textarea,
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

  document.addEventListener("DOMContentLoaded", function () {
    init(document);
  });
  ["htmx:after:swap", "htmx:afterSwap"].forEach(function (name) {
    document.addEventListener(name, function () {
      init(document);
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
