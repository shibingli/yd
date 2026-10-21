/*
 * yd-cms.js — 内容管理的浏览器增强：复制媒体地址与编辑器图片上传。
 * yd-cms.js — CMS browser enhancements: copying media URLs and uploading editor images.
 *
 * 页面只通过 .PageScripts 在内容管理页面加载本文件；它不依赖任何全局变量，也不发起 CDN 请求。
 * 编辑器实例由 yd-message-editor.js 创建，本文件只在实例就绪后追加一个上传按钮。
 * Pages load this file through .PageScripts on CMS pages only; it depends on no global and makes no CDN request.
 * The editor instance is created by yd-message-editor.js and this file only appends an upload button once it is ready.
 */
(function () {
  "use strict";

  var READY = "ydCmsUploadReady";

  /* copyText 优先使用异步剪贴板，失败时退回 execCommand。 | copyText prefers the async clipboard and falls back to execCommand. */
  function copyText(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise(function (resolve, reject) {
      var area = document.createElement("textarea");
      area.value = text;
      area.setAttribute("readonly", "");
      area.style.position = "fixed";
      area.style.opacity = "0";
      document.body.appendChild(area);
      area.select();
      try {
        if (document.execCommand("copy")) {
          resolve();
        } else {
          reject(new Error("copy failed"));
        }
      } catch (error) {
        reject(error);
      } finally {
        document.body.removeChild(area);
      }
    });
  }

  /* notify 通过应用已有的 toast 事件反馈结果。 | notify reports the result through the application's existing toast event. */
  function notify(text) {
    document.dispatchEvent(new CustomEvent("yd:toast", {
      detail: { level: "info", text: text }
    }));
  }

  /* bindCopy 接管媒体卡片上的复制按钮。 | bindCopy handles the copy buttons on media cards. */
  function bindCopy(root) {
    var buttons = root.querySelectorAll("[data-yd-copy]");
    for (var i = 0; i < buttons.length; i++) {
      var button = buttons[i];
      if (button.getAttribute("data-yd-copy-bound") === "1") {
        continue;
      }
      button.setAttribute("data-yd-copy-bound", "1");
      button.addEventListener("click", function (event) {
        var target = event.currentTarget;
        var value = target.getAttribute("data-yd-copy") || "";
        if (!value) {
          return;
        }
        copyText(value).then(function () {
          notify(value);
        }).catch(function () {
          window.prompt("", value);
        });
      });
    }
  }

  /* insertImage 把上传结果插入 Markdown 光标处。 | insertImage inserts the upload result at the Markdown cursor. */
  function insertImage(editor, url, name) {
    var alt = (name || "image").replace(/[\[\]()]/g, "");
    editor.codemirror.replaceSelection("![" + alt + "](" + url + ")\n");
    editor.codemirror.focus();
  }

  /* upload 把文件发送到服务端并插入返回的地址。 | upload sends the file and inserts the returned address. */
  function upload(element, editor, file) {
    var endpoint = element.getAttribute("data-yd-editor-upload");
    if (!endpoint || !file) {
      return;
    }
    var body = new FormData();
    body.append("file", file);
    body.append("alt", file.name || "");
    fetch(endpoint, { method: "POST", body: body, credentials: "same-origin" }).then(function (response) {
      /* 会话过期或网关故障时响应可能是 HTML，先按文本读取再尝试解析 JSON，
         避免把 “Unexpected token <” 当成上传错误展示。 |
         An expired session or a gateway failure can answer HTML; read text first and only then try JSON,
         so "Unexpected token <" is never shown as the upload error. */
      return response.text().then(function (text) {
        var payload = null;
        try {
          payload = text ? JSON.parse(text) : null;
        } catch (error) {
          payload = null;
        }
        if (!response.ok) {
          throw new Error(payload && payload.error ? payload.error : (text || "upload failed"));
        }
        return payload;
      });
    }).then(function (payload) {
      if (payload && payload.url) {
        insertImage(editor, payload.url, payload.name || file.name);
      }
    }).catch(function (error) {
      notify(error && error.message ? error.message : "upload failed");
    });
  }

  /* enhance 给带 data-yd-editor-upload 的编辑器追加一个上传按钮。 | enhance appends an upload button to editors carrying data-yd-editor-upload. */
  function enhance(root) {
    var elements = root.querySelectorAll("[data-yd-editor-upload]");
    for (var i = 0; i < elements.length; i++) {
      var element = elements[i];
      if (element.getAttribute(READY) === "1") {
        continue;
      }
      var editor = element.__ydEditor;
      var toolbar = element.querySelector(".editor-toolbar");
      if (!editor || !toolbar) {
        continue;
      }
      element.setAttribute(READY, "1");
      (function (owner, instance) {
        var button = document.createElement("button");
        button.type = "button";
        button.className = "fa fa-upload";
        button.setAttribute("aria-label", "Upload image");
        button.title = "Upload image";
        button.addEventListener("click", function (event) {
          event.preventDefault();
          var input = document.createElement("input");
          input.type = "file";
          input.accept = "image/*";
          input.addEventListener("change", function () {
            if (input.files && input.files[0]) {
              upload(owner, instance, input.files[0]);
            }
          });
          input.click();
        });
        toolbar.appendChild(button);
      })(element, editor);
    }
  }

  function boot() {
    bindCopy(document);
    enhance(document);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
  document.addEventListener("htmx:after:swap", function () {
    boot();
  });
  document.addEventListener("htmx:afterSwap", function () {
    boot();
  });
})();
