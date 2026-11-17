/* yd-app.user.js — 框架兜底的空用户脚本层。 | the framework's empty fallback user script layer.

   生成项目会在主脚本 yd-app.js 之后加载 `yd-app.user.js`。分层资源出现之前创建的项目没有项目副本，
   合并静态树因此在 /static 下提供这个空脚本，保证基础模板与生产 bundle 都不会 404；项目自己的同名
   文件始终优先，`yd sync` 也永不覆盖它。
   A generated project loads `yd-app.user.js` after the main yd-app.js. Projects created before the
   layered assets existed have no project copy, so the merged static tree serves this no-op under
   /static and neither the base template nor the production bundle can hit a 404. A project's own file
   always shadows it, and `yd sync` never overwrites that file.

   项目自定义请写进项目自己的 yd-app.user.js；这里只保留兜底，不应加入业务逻辑。
   Put project customizations in the project's own yd-app.user.js; this file is a fallback only and
   must not carry business logic. */
(function () {
  "use strict";
})();