/*!
 * yd-ui.js — the behaviour layer of the yd UI kit: Alpine components and the China-specific
 * validation/formatting kernels. | yd-ui.js — yd UI 组件层的行为层：Alpine 组件与中国常用控件的
 * 校验/格式化内核。
 *
 * Why Alpine and htmx together | 为什么 Alpine 与 htmx 并存
 *   htmx owns every piece of state that lives on the server: lists, records, filters, pagination,
 *   navigation and mutations. Alpine owns state that never leaves the page: whether a menu is open,
 *   which tab is active, how a phone number is formatted while it is being typed, which rows are
 *   ticked. Neither mirrors the other: the server never asks Alpine what it already rendered, and
 *   Alpine never fetches data. When a htmx swap replaces a fragment, Alpine's own mutation observer
 *   initialises the new components, so no re-binding code exists here.
 *   htmx 负责一切存在于服务端的状态：列表、记录、筛选、分页、导航与变更。Alpine 负责从不离开页面的
 *   状态：菜单是否展开、哪个页签处于激活、手机号输入时的格式化、勾选了哪些行。两者互不镜像：
 *   服务端从不向 Alpine 询问它已经渲染过的内容，Alpine 也从不自行取数。htmx 换入片段后，
 *   Alpine 自带的变更观察器会初始化新组件，因此本文件没有任何「重新绑定」代码。
 *
 * CSP constraint | CSP 约束
 *   Generated projects ship `script-src 'self'` with no `unsafe-eval`, so this file is designed for
 *   the Alpine CSP build: every component is registered through Alpine.data(), and templates only
 *   use the expression subset that build supports (property access, comparisons, ternaries,
 *   assignment and method calls). No component ships inline JavaScript.
 *   生成项目的内容安全策略是 `script-src 'self'`，不含 `unsafe-eval`，因此本文件按 Alpine 的 CSP
 *   构建设计：所有组件经 Alpine.data() 注册，模板只使用该构建支持的表达式子集（属性访问、比较、
 *   三元、赋值与方法调用）。没有任何组件携带内联 JavaScript。
 *
 * Progressive enhancement | 渐进增强
 *   Every control renders a real, named form field. Before Alpine initialises — and after any htmx
 *   swap — the field still submits. The script only sanitises, validates and formats around it; a
 *   control that hid the submitted value in a script-only field would break exactly when the script
 *   fails to load.
 *   每个控件都渲染真实、带 name 的表单字段。Alpine 初始化之前、以及任何一次 htmx 交换之后，
 *   字段依旧可以提交。脚本只在其外围做净化、校验与格式化；把提交值藏进「只有脚本才能生成」的
 *   字段，会在脚本加载失败时恰好坏掉。
 *
 * Public surface | 对外接口
 *   window.ydUI                 { version, components, register(Alpine) }
 *   window.ydUI.CN              validators and formatters, also exposed as window.ydCN
 *   window.ydCN.phone(v)        mainland mobile number, 11 digits | 大陆手机号，11 位
 *   window.ydCN.idCard(v)       resident ID card checksum (GB 11643) | 居民身份证校验（GB 11643）
 *   window.ydCN.bankCard(v)     Luhn + a best-effort issuer lookup | Luhn 校验与尽力而为的发卡行识别
 *   window.ydCN.creditCode(v)   unified social credit code (GB 32100) | 统一社会信用代码（GB 32100）
 *   window.ydCN.amountToChinese(v)  RMB amount in Chinese capitalised form | 人民币中文大写
 *
 * Markup contract | 标记契约
 *   x-data="ydPhone" etc.; options are read from data-* on the same element, because the CSP build
 *   evaluates object literals but not arbitrary constructor calls spread across attributes.
 *   x-data="ydPhone" 等；选项读取同一元素上的 data-*，因为 CSP 构建支持对象字面量，但不支持把
 *   构造函数调用拆散到各个属性里。
 */
(function (root, factory) {
  'use strict';
  if (typeof module === 'object' && module.exports) {
    module.exports = factory();
    return;
  }
  var api = factory();
  root.ydUI = api;
  // The validators get a short alias of their own: templates and application scripts read
  // ydCN.phone(...) far more often than they read the registry.
  // 校验器另给一个短别名：模板与应用脚本读 ydCN.phone(...) 的次数远多于读注册表本身。
  root.ydCN = api.CN;
  if (root.yd && typeof root.yd === 'object' && !root.yd.cn) {
    root.yd.cn = api.CN;
  }
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  var g = typeof globalThis !== 'undefined' ? globalThis : {};
  var VERSION = '1.0.0';

  /* ------------------------------------------------------------------------------------------------
     Small utilities | 小工具
     ------------------------------------------------------------------------------------------------ */

  // digits strips everything that is not a decimal digit. | digits 去掉所有非十进制数字字符。
  function digits(value) {
    return String(value === undefined || value === null ? '' : value).replace(/\D+/g, '');
  }

  // pad2 left-pads a number to two digits. | pad2 把数字左补零到两位。
  function pad2(n) {
    return (n < 10 ? '0' : '') + n;
  }

  // ymd formats a Date as YYYY-MM-DD in local time. | ymd 按本地时间把 Date 格式化为 YYYY-MM-DD。
  function ymd(date) {
    return date.getFullYear() + '-' + pad2(date.getMonth() + 1) + '-' + pad2(date.getDate());
  }

  // parseYmd parses YYYY-MM-DD into a local Date, or null. | parseYmd 把 YYYY-MM-DD 解析为本地 Date，失败返回 null。
  function parseYmd(value) {
    var m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(value || ''));
    if (!m) {
      return null;
    }
    var d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
    if (d.getFullYear() !== Number(m[1]) || d.getMonth() !== Number(m[2]) - 1 || d.getDate() !== Number(m[3])) {
      return null;
    }
    return d;
  }

  // today returns midnight of the current local day. | today 返回当前本地日期的零点。
  function today() {
    var d = new Date();
    return new Date(d.getFullYear(), d.getMonth(), d.getDate());
  }

  // addDays returns a new date shifted by n days. | addDays 返回平移 n 天后的新日期。
  function addDays(date, n) {
    var d = new Date(date.getFullYear(), date.getMonth(), date.getDate());
    d.setDate(d.getDate() + n);
    return d;
  }

  // sizeText renders a byte count with the units a Chinese operator expects. | sizeText 用中文运维熟悉的
  // 单位渲染字节数。
  function sizeText(bytes) {
    var n = Number(bytes) || 0;
    if (n < 1024) {
      return n + ' B';
    }
    if (n < 1024 * 1024) {
      return (n / 1024).toFixed(1) + ' KB';
    }
    if (n < 1024 * 1024 * 1024) {
      return (n / (1024 * 1024)).toFixed(1) + ' MB';
    }
    return (n / (1024 * 1024 * 1024)).toFixed(2) + ' GB';
  }

  /* ------------------------------------------------------------------------------------------------
     China-specific kernels | 中国常用控件内核
     ------------------------------------------------------------------------------------------------ */

  var CN = {};

  // phone reports whether v is an 11-digit mainland mobile number. | phone 判断 v 是否为 11 位大陆手机号。
  CN.phone = function (v) {
    return /^1[3-9]\d{9}$/.test(digits(v));
  };

  // phoneFormatted groups an 11-digit number as 3-4-4. | phoneFormatted 把 11 位号码按 3-4-4 分组。
  CN.phoneFormatted = function (v) {
    var d = digits(v).slice(0, 11);
    if (!d) {
      return '';
    }
    if (d.length <= 3) {
      return d;
    }
    if (d.length <= 7) {
      return d.slice(0, 3) + ' ' + d.slice(3);
    }
    return d.slice(0, 3) + ' ' + d.slice(3, 7) + ' ' + d.slice(7);
  };

  // ID_WEIGHTS and ID_CHECK are the GB 11643 check-digit tables. | ID_WEIGHTS 与 ID_CHECK 是 GB 11643
  // 的校验位表。
  var ID_WEIGHTS = [7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2];
  var ID_CHECK = '10X98765432';

  // CN.idCard reports whether v is an 18-digit resident ID card with a valid checksum and birth
  // date. The 15-digit legacy form is deliberately rejected: it carries no century and no
  // check digit, so accepting it would make the validation weaker than the data it inspects.
  // CN.idCard 判断 v 是否为 18 位居民身份证，且校验位与出生日期均有效。刻意不接受 15 位旧式号码：
  // 它既没有世纪位也没有校验位，接受它会让校验比被检查的数据本身还弱。
  CN.idCard = function (v) {
    var s = String(v || '').trim().toUpperCase();
    if (!/^\d{17}[\dX]$/.test(s)) {
      return false;
    }
    var sum = 0;
    for (var i = 0; i < 17; i++) {
      sum += Number(s.charAt(i)) * ID_WEIGHTS[i];
    }
    if (ID_CHECK.charAt(sum % 11) !== s.charAt(17)) {
      return false;
    }
    return CN.idCardInfo(s) !== null;
  };

  // CN.idCardInfo derives the birth date, sex and age from a valid ID card, or returns null.
  // CN.idCardInfo 从有效身份证推导出生日期、性别与年龄；无效时返回 null。
  CN.idCardInfo = function (v) {
    var s = String(v || '').trim().toUpperCase();
    if (!/^\d{17}[\dX]$/.test(s)) {
      return null;
    }
    var year = Number(s.slice(6, 10));
    var month = Number(s.slice(10, 12));
    var day = Number(s.slice(12, 14));
    var birth = parseYmd(year + '-' + pad2(month) + '-' + pad2(day));
    if (!birth || year < 1900 || birth > today()) {
      return null;
    }
    var now = today();
    var age = now.getFullYear() - year;
    if (now.getMonth() < birth.getMonth() || (now.getMonth() === birth.getMonth() && now.getDate() < birth.getDate())) {
      age--;
    }
    if (age < 0 || age > 150) {
      return null;
    }
    return {
      birth: ymd(birth),
      age: age,
      sex: Number(s.charAt(16)) % 2 === 1 ? 'male' : 'female'
    };
  };

  // CN.luhn is the Luhn check shared by bank cards. | CN.luhn 是银行卡共用的 Luhn 校验。
  CN.luhn = function (v) {
    var s = digits(v);
    if (s.length < 12 || s.length > 19) {
      return false;
    }
    var sum = 0;
    var double = false;
    for (var i = s.length - 1; i >= 0; i--) {
      var n = Number(s.charAt(i));
      if (double) {
        n *= 2;
        if (n > 9) {
          n -= 9;
        }
      }
      sum += n;
      double = !double;
    }
    return sum % 10 === 0;
  };

  // BANKS is a best-effort issuer table keyed by the first six digits. It is deliberately short:
  // the full BIN registry changes constantly, and a wrong issuer is worse than no issuer, so a
  // miss renders nothing rather than a guess. | BANKS 是按前六位索引的尽力而为发卡行表。它刻意很短：
  // 完整 BIN 库一直在变，而「报错行名」比「不报行名」更糟，所以未命中时什么都不显示，而不是猜测。
  var BANKS = [
    ['622202', '中国工商银行'],
    ['622203', '中国工商银行'],
    ['621226', '中国工商银行'],
    ['955880', '中国工商银行'],
    ['622848', '中国农业银行'],
    ['622845', '中国农业银行'],
    ['955998', '中国农业银行'],
    ['621661', '中国银行'],
    ['621660', '中国银行'],
    ['456351', '中国银行'],
    ['621700', '中国建设银行'],
    ['436742', '中国建设银行'],
    ['622260', '交通银行'],
    ['622262', '交通银行'],
    ['622588', '招商银行'],
    ['621483', '招商银行'],
    ['622155', '上海浦东发展银行'],
    ['622156', '上海浦东发展银行'],
    ['621792', '上海浦东发展银行'],
    ['622521', '上海浦东发展银行'],
    ['622908', '兴业银行'],
    ['622909', '兴业银行'],
    ['622630', '华夏银行'],
    ['622631', '华夏银行'],
    ['622660', '中国光大银行'],
    ['622668', '广发银行'],
    ['622998', '平安银行'],
    ['622280', '中信银行'],
    ['622690', '中信银行'],
    ['622150', '中国邮政储蓄银行'],
    ['621599', '中国邮政储蓄银行']
  ];

  // CN.bankCard validates a bank card number with Luhn. | CN.bankCard 用 Luhn 校验银行卡号。
  CN.bankCard = function (v) {
    return CN.luhn(v);
  };

  // CN.bankName returns the issuer for a known BIN, or an empty string. | CN.bankName 返回已知 BIN 的
  // 发卡行，未知时返回空串。
  CN.bankName = function (v) {
    var s = digits(v);
    if (s.length < 6) {
      return '';
    }
    var prefix = s.slice(0, 6);
    for (var i = 0; i < BANKS.length; i++) {
      if (BANKS[i][0] === prefix) {
        return BANKS[i][1];
      }
    }
    return '';
  };

  // CN.bankCardFormatted groups a card number in fours, for a preview only. | CN.bankCardFormatted
  // 每四位分组，仅用于预览。
  CN.bankCardFormatted = function (v) {
    var s = digits(v).slice(0, 19);
    var out = [];
    for (var i = 0; i < s.length; i += 4) {
      out.push(s.slice(i, i + 4));
    }
    return out.join(' ');
  };

  // CREDIT_CHARSET and CREDIT_WEIGHTS are the GB 32100 tables. | CREDIT_CHARSET 与 CREDIT_WEIGHTS 是
  // GB 32100 的校验表。
  var CREDIT_CHARSET = '0123456789ABCDEFGHJKLMNPQRTUWXY';
  var CREDIT_WEIGHTS = [1, 3, 9, 27, 19, 26, 16, 17, 20, 29, 25, 13, 8, 24, 10, 30, 28];

  // CN.creditCode validates an 18-character unified social credit code. | CN.creditCode 校验 18 位
  // 统一社会信用代码。
  CN.creditCode = function (v) {
    var s = String(v || '').trim().toUpperCase();
    if (!/^[0-9A-HJ-NPQRTUWXY]{18}$/.test(s)) {
      return false;
    }
    var sum = 0;
    for (var i = 0; i < 17; i++) {
      var idx = CREDIT_CHARSET.indexOf(s.charAt(i));
      if (idx < 0) {
        return false;
      }
      sum += idx * CREDIT_WEIGHTS[i];
    }
    var mod = sum % 31;
    var check = CREDIT_CHARSET.charAt((31 - mod) % 31);
    return check === s.charAt(17);
  };

  // AMOUNT_DIGITS / UNITS / GROUPS are the Chinese capitalisation tables. | AMOUNT_DIGITS / UNITS /
  // GROUPS 是中文大写金额的字符表。
  var AMOUNT_DIGITS = '零壹贰叁肆伍陆柒捌玖';
  var AMOUNT_UNITS = ['', '拾', '佰', '仟'];
  var AMOUNT_GROUPS = ['', '万', '亿', '万亿'];

  // CN.sanitizeAmount keeps digits, at most one dot and at most two decimals. A leading minus is
  // preserved rather than dropped: turning "-5" into "5" would silently flip the sign of a refund
  // or an adjustment, and an invalid amount must stay visibly invalid for the server to reject.
  // CN.sanitizeAmount 只保留数字、至多一个小数点与至多两位小数。开头的负号会被保留而不是丢弃：
  // 把 "-5" 变成 "5" 会悄悄翻转退款或调整的符号，非法金额必须保持可见的非法状态，交给服务端拒绝。
  CN.sanitizeAmount = function (v) {
    var raw = String(v === undefined || v === null ? '' : v).trim();
    var negative = raw.charAt(0) === '-';
    var s = raw.replace(/[^\d.]/g, '');
    var dot = s.indexOf('.');
    if (dot >= 0) {
      s = s.slice(0, dot + 1) + s.slice(dot + 1).replace(/\./g, '');
    }
    var parts = s.split('.');
    var intPart = parts[0].replace(/^0+(?=\d)/, '').slice(0, 12);
    var out = parts.length === 1 ? intPart : intPart + '.' + parts[1].slice(0, 2);
    if (negative && out !== '') {
      return '-' + out;
    }
    return out;
  };

  // CN.amountToChinese renders a non-negative RMB amount in the capitalised form banks expect.
  // Input is a string to avoid binary floating point changing the last fen; anything that is not a
  // plain non-negative decimal returns an empty string, so a malformed value never becomes a
  // confident-looking cheque.
  // CN.amountToChinese 把非负人民币金额渲染为银行使用的中文大写。输入用字符串，避免二进制浮点改变
  // 最后一分；不是普通非负小数的输入返回空串，因此畸形值绝不会变成一张看起来可信的支票。
  CN.amountToChinese = function (value) {
    var raw = String(value === undefined || value === null ? '' : value).replace(/,/g, '').trim();
    if (!/^\d+(?:\.\d{0,2})?$/.test(raw)) {
      return '';
    }
    var parts = raw.split('.');
    var intPart = parts[0].replace(/^0+(?=\d)/, '');
    var dec = (parts[1] || '').replace(/\D/g, '').slice(0, 2);
    while (dec.length < 2) {
      dec += '0';
    }
    if (intPart.length > 16) {
      return '';
    }

    // Convert the integer part group by group (four digits per group) so that 万/亿/万亿 attach
    // after the group they belong to instead of after every digit.
    // 按每四位一组转换整数部分，使 万/亿/万亿 挂在所属整组之后，而不是挂在每一位之后。
    var groups = [];
    var rest = intPart;
    while (rest.length > 0) {
      groups.unshift(rest.slice(-4));
      rest = rest.slice(0, -4);
    }

    var intText = '';
    var zeroBetween = false;
    for (var gi = 0; gi < groups.length; gi++) {
      var group = groups[gi];
      var groupText = '';
      var zeroInGroup = false;
      for (var j = 0; j < group.length; j++) {
        var digit = Number(group.charAt(j));
        var unit = group.length - 1 - j;
        if (digit === 0) {
          if (groupText !== '') {
            zeroInGroup = true;
          }
          continue;
        }
        if (zeroInGroup) {
          groupText += '零';
          zeroInGroup = false;
        }
        groupText += AMOUNT_DIGITS.charAt(digit) + AMOUNT_UNITS[unit];
      }
      if (groupText !== '') {
        if (intText !== '' && (zeroBetween || (group.length === 4 && group.charAt(0) === '0'))) {
          intText += '零';
        }
        intText += groupText + AMOUNT_GROUPS[groups.length - 1 - gi];
        zeroBetween = false;
      } else if (intText !== '') {
        zeroBetween = true;
      }
    }
    if (intText === '') {
      intText = '零';
    }

    var jiao = Number(dec.charAt(0));
    var fen = Number(dec.charAt(1));
    var out = intText + '元';
    if (jiao === 0 && fen === 0) {
      return out + '整';
    }
    if (jiao > 0) {
      out += AMOUNT_DIGITS.charAt(jiao) + '角';
    } else {
      out += '零';
    }
    if (fen > 0) {
      out += AMOUNT_DIGITS.charAt(fen) + '分';
    }
    return out;
  };

  // CN.provinces is the built-in first level of the region cascader. A full province/city/district
  // dataset is deliberately not vendored: it changes every year and would dominate the bundle.
  // Applications inject their own dataset through <script type="application/json" id="..."> and
  // point the component at it with data-yd-region-source="#id".
  // CN.provinces 是省市区级联的内置第一级。刻意不内置完整省市县数据集：它每年都在变，且会主导
  // 打包体积。应用通过 <script type="application/json" id="..."> 注入自己的数据，并用
  // data-yd-region-source="#id" 指向它。
  CN.provinces = [
    { code: '110000', name: '北京市' },
    { code: '120000', name: '天津市' },
    { code: '130000', name: '河北省' },
    { code: '140000', name: '山西省' },
    { code: '150000', name: '内蒙古自治区' },
    { code: '210000', name: '辽宁省' },
    { code: '220000', name: '吉林省' },
    { code: '230000', name: '黑龙江省' },
    { code: '310000', name: '上海市' },
    { code: '320000', name: '江苏省' },
    { code: '330000', name: '浙江省' },
    { code: '340000', name: '安徽省' },
    { code: '350000', name: '福建省' },
    { code: '360000', name: '江西省' },
    { code: '370000', name: '山东省' },
    { code: '410000', name: '河南省' },
    { code: '420000', name: '湖北省' },
    { code: '430000', name: '湖南省' },
    { code: '440000', name: '广东省' },
    { code: '450000', name: '广西壮族自治区' },
    { code: '460000', name: '海南省' },
    { code: '500000', name: '重庆市' },
    { code: '510000', name: '四川省' },
    { code: '520000', name: '贵州省' },
    { code: '530000', name: '云南省' },
    { code: '540000', name: '西藏自治区' },
    { code: '610000', name: '陕西省' },
    { code: '620000', name: '甘肃省' },
    { code: '630000', name: '青海省' },
    { code: '640000', name: '宁夏回族自治区' },
    { code: '650000', name: '新疆维吾尔自治区' },
    { code: '710000', name: '台湾省' },
    { code: '810000', name: '香港特别行政区' },
    { code: '820000', name: '澳门特别行政区' }
  ];

  /* ------------------------------------------------------------------------------------------------
     Clipboard helper | 剪贴板辅助
     ------------------------------------------------------------------------------------------------ */

  // copyText copies text, preferring the async clipboard API and falling back to a temporary
  // textarea for older browsers and non-secure contexts. | copyText 复制文本，优先使用异步剪贴板 API，
  // 旧浏览器与非安全上下文回退到临时 textarea。
  function copyText(text) {
    var d = g.document;
    if (!text) {
      return Promise.reject(new Error('empty | 内容为空'));
    }
    if (g.navigator && g.navigator.clipboard && typeof g.navigator.clipboard.writeText === 'function') {
      return g.navigator.clipboard.writeText(text);
    }
    if (!d || typeof d.createElement !== 'function') {
      return Promise.reject(new Error('clipboard unavailable | 剪贴板不可用'));
    }
    return new Promise(function (resolve, reject) {
      var area = d.createElement('textarea');
      area.value = text;
      area.setAttribute('readonly', 'readonly');
      area.style.position = 'fixed';
      area.style.opacity = '0';
      d.body.appendChild(area);
      area.select();
      try {
        var ok = d.execCommand && d.execCommand('copy');
        d.body.removeChild(area);
        if (ok) {
          resolve();
          return;
        }
      } catch (err) {
        if (area.parentNode) {
          area.parentNode.removeChild(area);
        }
        reject(err);
        return;
      }
      reject(new Error('copy rejected | 复制被拒绝'));
    });
  }

  /* ------------------------------------------------------------------------------------------------
     Alpine components | Alpine 组件
     ------------------------------------------------------------------------------------------------ */

  // components is the registry this file installs; it is also what ydUI.components lists. | components
  // 是本文件安装的注册表，也是 ydUI.components 列出的内容。
  var components = {};

  // registerCopy: copy a literal, or the value/text of a target selector. | registerCopy：复制字面值，
  // 或目标选择器的 value/text。
  components.ydCopy = function () {
    return {
      copied: false,
      error: false,
      timer: 0,
      init: function () {
        this.copied = false;
        this.error = false;
      },
      destroy: function () {
        if (this.timer) {
          clearTimeout(this.timer);
        }
      },
      copy: function () {
        var el = this.$el;
        var text = el.getAttribute('data-yd-copy') || '';
        var selector = el.getAttribute('data-yd-copy-target') || '';
        if (selector) {
          var target = el.ownerDocument.querySelector(selector);
          if (target) {
            text = target.value !== undefined && target.value !== '' ? target.value : target.textContent || '';
          }
        }
        var self = this;
        copyText(text)
          .then(function () {
            self.copied = true;
            self.error = false;
            self.arm();
            self.$dispatch('yd:copy', { value: text });
          })
          .catch(function () {
            self.copied = false;
            self.error = true;
            self.arm();
          });
      },
      arm: function () {
        var self = this;
        if (this.timer) {
          clearTimeout(this.timer);
        }
        this.timer = setTimeout(function () {
          self.copied = false;
          self.error = false;
        }, 1800);
      }
    };
  };

  // registerDropdown: an accessible menu with outside-click, Escape and roving arrow keys. The
  // trigger and the panel share one keyboard handler, so the menu can be driven without a mouse
  // whether focus is still on the trigger or has moved into the items.
  // registerDropdown：支持外部点击、Escape 与方向键漫游的可访问菜单。触发器与面板共用一个键盘
  // 处理器，因此无论焦点仍在触发器还是已进入条目，都可以不用鼠标操作。
  components.ydDropdown = function () {
    return {
      open: false,
      init: function () {
        var self = this;
        this._outside = function (event) {
          if (self.open && !(self.$root || self.$el).contains(event.target)) {
            self.close(false);
          }
        };
        (this.$root || this.$el).ownerDocument.addEventListener('click', this._outside);
      },
      destroy: function () {
        if (this._outside) {
          (this.$root || this.$el).ownerDocument.removeEventListener('click', this._outside);
        }
      },
      toggle: function () {
        if (this.open) {
          this.close(true);
          return;
        }
        this.open = true;
        this.focusItem(0);
      },
      close: function (restore) {
        this.open = false;
        if (restore && this.$refs.trigger && typeof this.$refs.trigger.focus === 'function') {
          this.$refs.trigger.focus();
        }
      },
      items: function () {
        return Array.prototype.slice.call((this.$root || this.$el).querySelectorAll('[role="menuitem"]'));
      },
      focusItem: function (index) {
        var self = this;
        this.$nextTick(function () {
          var items = self.items();
          if (!items.length) {
            return;
          }
          var picked = ((index % items.length) + items.length) % items.length;
          items[picked].focus();
        });
      },
      move: function (event, step) {
        var list = this.items();
        var current = list.indexOf(event.target && event.target.closest ? event.target.closest('[role="menuitem"]') : event.target);
        if (current < 0) {
          current = step > 0 ? -1 : 0;
        }
        this.focusItem(current + step);
      },
      onKey: function (event) {
        var key = event && event.key;
        if (key === 'Escape' || key === 'Esc') {
          event.preventDefault();
          this.close(true);
          return;
        }
        if (key === 'ArrowDown' || key === 'Down') {
          event.preventDefault();
          if (!this.open) {
            this.open = true;
            this.focusItem(0);
          } else {
            this.move(event, 1);
          }
          return;
        }
        if (key === 'ArrowUp' || key === 'Up') {
          event.preventDefault();
          if (!this.open) {
            this.open = true;
            this.focusItem(-1);
          } else {
            this.move(event, -1);
          }
          return;
        }
        if (key === 'Home' && this.open) {
          event.preventDefault();
          this.focusItem(0);
        } else if (key === 'End' && this.open) {
          event.preventDefault();
          this.focusItem(-1);
        }
      }
    };
  };

  // registerTabs: client-side tab state. Server-driven tab content uses one htmx request per
  // panel instead; this component only decides which rendered panel is visible.
  // registerTabs：客户端页签状态。服务端驱动的页签内容改成每个面板一次 htmx 请求；本组件只决定
  // 已渲染面板中哪一个可见。
  components.ydTabs = function () {
    return {
      active: '',
      init: function () {
        var first = (this.$root || this.$el).querySelector('[role="tab"]');
        this.active = this.$el.getAttribute('data-active') || (first ? first.getAttribute('data-tab') : '');
      },
      select: function (id) {
        this.active = id;
        this.$dispatch('yd:tabs:change', { id: id });
      },
      isActive: function (id) {
        return this.active === id;
      },
      move: function (event, step) {
        var tabs = Array.prototype.slice.call((this.$root || this.$el).querySelectorAll('[role="tab"]'));
        var node = event && event.target && event.target.closest ? event.target.closest('[role="tab"]') : event.target;
        var current = tabs.indexOf(node);
        if (current < 0) {
          return;
        }
        var next = tabs[((current + step) % tabs.length + tabs.length) % tabs.length];
        if (next) {
          next.focus();
          var id = next.getAttribute('data-tab');
          if (id) {
            this.select(id);
          }
        }
      },
      onKey: function (event) {
        var key = event && event.key;
        if (key === 'Home' || key === 'End') {
          var tabs = Array.prototype.slice.call((this.$root || this.$el).querySelectorAll('[role="tab"]'));
          if (!tabs.length) {
            return;
          }
          event.preventDefault();
          var picked = tabs[key === 'Home' ? 0 : tabs.length - 1];
          picked.focus();
          var pickedId = picked.getAttribute('data-tab');
          if (pickedId) {
            this.select(pickedId);
          }
        }
      }
    };
  };

  // registerTableSelect: a select-all checkbox for a table body. It never touches the server; the
  // checked rows are ordinary form fields and submit with the form. A partial selection is drawn as
  // the native indeterminate state and the next click selects everything, which is what an operator
  // expects after ticking a few rows by hand.
  // registerTableSelect：表格体的全选复选框。它从不联系服务端；被勾选的行就是普通表单字段，
  // 随表单一起提交。部分选中以原生 indeterminate 状态呈现，下一次点击选中全部——这正是操作员
  // 手选了几行之后期望的行为。
  components.ydTableSelect = function () {
    return {
      all: false,
      partial: false,
      count: 0,
      init: function () {
        this.sync();
      },
      boxes: function () {
        return Array.prototype.slice.call((this.$root || this.$el).querySelectorAll('tbody input[type="checkbox"][name]'));
      },
      sync: function () {
        var boxes = this.boxes();
        var checked = boxes.filter(function (box) {
          return box.checked;
        }).length;
        this.count = checked;
        this.partial = checked > 0 && checked < boxes.length;
        this.all = boxes.length > 0 && checked === boxes.length;
        var master = this.$refs.all;
        if (master) {
          master.checked = this.all;
          master.indeterminate = this.partial;
        }
      },
      toggleAll: function () {
        // A partial selection means "select the rest", not "clear everything". | 部分选中意味着
        // 「补齐剩余」，而不是「全部清空」。
        var check = this.count !== this.boxes().length;
        this.boxes().forEach(function (box) {
          box.checked = check;
        });
        this.sync();
      }
    };
  };

  // registerSelect: replace the native dropdown's painted surface with a listbox while keeping the
  // real <select> in the form. Without JavaScript the native control remains; with JavaScript the
  // native select is hidden but still submits the same name/value pair.
  // registerSelect：用列表框替换原生下拉的可见表面，同时把真实 <select> 留在表单里。没有 JavaScript
  // 时原生控件仍在；有 JavaScript 时原生 select 被隐藏，但仍提交同一个 name/value。
  components.ydSelect = function () {
    return {
      enhanced: false,
      open: false,
      options: [],
      selectedLabel: '',
      activeIndex: -1,
      init: function () {
        this.sync();
        this.enhanced = true;
        var native = this.$refs.native;
        var doc = (this.$root || this.$el).ownerDocument;
        if (native && native.hasAttribute('data-yd-theme-set') && doc && typeof doc.addEventListener === 'function') {
          var self = this;
          this._theme = function () {
            self.sync();
          };
          doc.addEventListener('yd:theme', this._theme);
        }
      },
      destroy: function () {
        var doc = (this.$root || this.$el).ownerDocument;
        if (this._theme && doc && typeof doc.removeEventListener === 'function') {
          doc.removeEventListener('yd:theme', this._theme);
          this._theme = null;
        }
      },
      sync: function () {
        var native = this.$refs.native;
        if (!native) {
          return;
        }
        var options = [];
        var selectedLabel = '';
        for (var i = 0; i < native.options.length; i++) {
          var option = native.options[i];
          var label = String(option.textContent || '').trim();
          options.push({
            value: option.value,
            label: label,
            disabled: option.disabled,
            selected: option.selected,
            active: false
          });
          if (option.selected) {
            selectedLabel = label;
          }
        }
        if (!selectedLabel && options.length) {
          selectedLabel = options[0].label;
        }
        this.options = options;
        this.selectedLabel = selectedLabel;
      },
      selectedIndex: function () {
        for (var i = 0; i < this.options.length; i++) {
          if (this.options[i].selected) {
            return i;
          }
        }
        return 0;
      },
      toggle: function () {
        var trigger = this.$refs.trigger;
        if (!trigger || trigger.disabled) {
          return;
        }
        this.sync();
        this.open = !this.open;
        if (this.open) {
          this.activeIndex = this.selectedIndex();
          this.focusActive();
        }
      },
      focusActive: function () {
        var self = this;
        this.$nextTick(function () {
          var menu = self.$refs.menu;
          if (!menu) {
            return;
          }
          var nodes = menu.querySelectorAll('[role="option"]');
          if (nodes[self.activeIndex]) {
            nodes[self.activeIndex].focus();
          }
        });
      },
      choose: function (value) {
        var native = this.$refs.native;
        if (!native) {
          return;
        }
        var option = null;
        for (var i = 0; i < this.options.length; i++) {
          if (this.options[i].value === value) {
            option = this.options[i];
            break;
          }
        }
        if (option && option.disabled) {
          return;
        }
        native.value = value;
        var doc = (this.$root || this.$el).ownerDocument;
        if (doc && typeof doc.createEvent === 'function') {
          var event = doc.createEvent('Event');
          event.initEvent('change', true, true);
          native.dispatchEvent(event);
        }
        this.sync();
        this.close(true);
      },
      move: function (delta) {
        if (!this.options.length) {
          return;
        }
        var index = this.activeIndex;
        for (var step = 0; step < this.options.length; step++) {
          index += delta;
          if (index < 0) {
            index = this.options.length - 1;
          }
          if (index >= this.options.length) {
            index = 0;
          }
          if (!this.options[index].disabled) {
            this.activeIndex = index;
            this.highlight();
            return;
          }
        }
      },
      highlight: function () {
        for (var i = 0; i < this.options.length; i++) {
          this.options[i].active = i === this.activeIndex;
        }
        this.focusActive();
      },
      onKey: function (event) {
        var key = event && event.key;
        if (key === 'Escape' || key === 'Esc') {
          event.preventDefault();
          this.close(true);
          return;
        }
        if (key === 'ArrowDown' || key === 'Down') {
          event.preventDefault();
          if (!this.open) {
            this.toggle();
          } else {
            this.move(1);
          }
          return;
        }
        if (key === 'ArrowUp' || key === 'Up') {
          event.preventDefault();
          if (!this.open) {
            this.toggle();
          } else {
            this.move(-1);
          }
          return;
        }
        if (key === 'Home' || key === 'End') {
          if (!this.open) {
            return;
          }
          event.preventDefault();
          this.activeIndex = key === 'Home' ? 0 : this.options.length - 1;
          this.highlight();
          return;
        }
        if ((key === 'Enter' || key === ' ' || key === 'Spacebar') && this.open) {
          event.preventDefault();
          var active = this.options[this.activeIndex];
          if (active) {
            this.choose(active.value);
          }
        }
      },
      close: function (restore) {
        this.open = false;
        this.activeIndex = -1;
        if (restore && this.$refs.trigger && typeof this.$refs.trigger.focus === 'function') {
          this.$refs.trigger.focus();
        }
      }
    };
  };

  // registerRows: clone the server-rendered blueprint for a repeatable row and remove rows down to
  // the minimum. The submitted names are ordinary `name[]` fields, so no index bookkeeping exists
  // on either side. | registerRows：克隆服务端渲染的可重复行蓝图，并删到最小行数为止。提交名是
  // 普通的 `name[]` 字段，因此两侧都不需要索引记账。
  components.ydRows = function () {
    return {
      min: 1,
      max: 20,
      init: function () {
        var root = this.$root || this.$el;
        var min = parseInt(root.getAttribute('data-min'), 10);
        var max = parseInt(root.getAttribute('data-max'), 10);
        this.min = isFinite(min) ? min : 1;
        this.max = isFinite(max) ? max : 20;
      },
      count: function () {
        var root = this.$refs.list || this.$el;
        return root.querySelectorAll('[data-yd-row]').length;
      },
      add: function () {
        if (this.count() >= this.max) {
          return;
        }
        var template = this.$refs.template;
        var list = this.$refs.list;
        if (!template || !list || !template.content || !template.content.firstElementChild) {
          return;
        }
        var row = template.content.firstElementChild.cloneNode(true);
        list.appendChild(row);
        this.$dispatch('yd:rows:add', { count: this.count() });
      },
      remove: function (element) {
        var row = element && element.closest ? element.closest('[data-yd-row]') : null;
        if (!row || this.count() <= this.min) {
          return;
        }
        row.parentNode.removeChild(row);
        this.$dispatch('yd:rows:remove', { count: this.count() });
      }
    };
  };

  // registerSearch: clear the field and re-fire the input event so an htmx trigger on the same
  // field re-queries immediately instead of waiting for the user to type again.
  // registerSearch：清空字段并重新派发 input 事件，使同一字段上的 htmx 触发器立即重新查询，
  // 而不是等用户再次输入。
  components.ydSearch = function () {
    return {
      value: '',
      init: function () {
        var input = this.$refs.input;
        this.value = input ? input.value : '';
      },
      clear: function () {
        var input = this.$refs.input;
        if (input) {
          input.value = '';
          input.dispatchEvent(new (input.ownerDocument.defaultView.Event)('input', { bubbles: true }));
          input.focus();
        }
        this.value = '';
        this.$dispatch('yd:search:clear');
      }
    };
  };

  // registerPassword: reveal/hide and a three-step strength hint. Strength is a local UI signal,
  // never a server-side policy decision. | registerPassword：显示/隐藏与三档强度提示。强度只是
  // 本地界面信号，绝不是服务端口径的策略判定。
  components.ydPassword = function () {
    return {
      show: false,
      value: '',
      init: function () {
        var input = this.$refs.input;
        this.value = input ? input.value : '';
      },
      toggle: function () {
        this.show = !this.show;
      },
      score: function () {
        var v = String(this.value || '');
        var n = 0;
        if (v.length >= 8) {
          n++;
        }
        if (/[A-Za-z]/.test(v) && /\d/.test(v)) {
          n++;
        }
        if (/[^A-Za-z0-9]/.test(v) && v.length >= 10) {
          n++;
        }
        return Math.min(n, 3);
      },
      label: function () {
        var n = this.score();
        if (!this.value) {
          return '';
        }
        return n <= 1 ? 'weak' : n === 2 ? 'fair' : 'strong';
      }
    };
  };

  // registerTagInput: a token field backed by a hidden comma-separated input. Commas inside a typed
  // or pasted value split into separate tags, and duplicates are dropped, so the hidden value stays
  // a clean list. A comma is therefore not allowed inside one tag; use a repeated field when it is.
  // registerTagInput：以逗号分隔的隐藏输入为后端的标签输入。输入或粘贴值中的逗号会拆成多个标签，
  // 重复项被丢弃，隐藏值因此保持为干净的列表。单个标签里因此不能含逗号；需要时请改用重复字段。
  components.ydTagInput = function () {
    return {
      tags: [],
      draft: '',
      max: 0,
      init: function () {
        var raw = this.$el.getAttribute('data-value') || '';
        this.max = parseInt(this.$el.getAttribute('data-max') || '0', 10) || 0;
        this.tags = raw
          .split(',')
          .map(function (item) {
            return item.trim();
          })
          .filter(function (item, index, list) {
            return item !== '' && list.indexOf(item) === index;
          });
      },
      add: function () {
        var value = String(this.draft || '').trim();
        if (!value) {
          return;
        }
        var parts = value.split(',');
        var changed = false;
        for (var i = 0; i < parts.length; i++) {
          var item = parts[i].trim();
          if (!item) {
            continue;
          }
          if (this.max > 0 && this.tags.length >= this.max) {
            break;
          }
          if (this.tags.indexOf(item) < 0) {
            this.tags.push(item);
            changed = true;
          }
        }
        this.draft = '';
        if (changed) {
          this.$dispatch('yd:tags:change', { tags: this.tags.slice() });
        }
      },
      remove: function (index) {
        this.tags.splice(index, 1);
        this.$dispatch('yd:tags:change', { tags: this.tags.slice() });
      },
      removeTag: function (tag) {
        var index = this.tags.indexOf(tag);
        if (index >= 0) {
          this.remove(index);
        }
      },
      value: function () {
        return this.tags.join(',');
      }
    };
  };

  // registerUpload: drag-and-drop and a file list. The real <input type="file"> remains the form
  // field, so htmx multipart uploads keep working exactly as without this component.
  // registerUpload：拖放与文件列表。真正的 <input type="file"> 仍是表单字段，因此 htmx 的
  // multipart 上传与没有本组件时完全一致。
  components.ydUpload = function () {
    return {
      dragging: false,
      files: [],
      init: function () {
        this.files = [];
      },
      onChange: function () {
        this.sync();
      },
      sync: function () {
        var input = this.$refs.input;
        this.files = input && input.files ? Array.prototype.slice.call(input.files) : [];
      },
      onDrop: function (event) {
        this.dragging = false;
        var input = this.$refs.input;
        var dropped = event.dataTransfer && event.dataTransfer.files;
        if (!input || !dropped || !dropped.length) {
          return;
        }
        try {
          var transfer = new DataTransfer();
          var droppedFiles = Array.prototype.slice.call(dropped);
          if (!input.multiple) {
            droppedFiles = droppedFiles.slice(0, 1);
          }
          var existing = input.multiple ? Array.prototype.slice.call(input.files || []) : [];
          existing.concat(droppedFiles).forEach(function (file) {
            transfer.items.add(file);
          });
          input.files = transfer.files;
        } catch (err) {
          // Assigning FileList is not universally supported; the browser still shows the dropped
          // files in its own picker, and the submit path stays intact.
          // 赋值 FileList 并非所有浏览器都支持；浏览器仍会在自己的选择器里显示拖入的文件，
          // 提交路径保持完好。
        }
        this.sync();
        input.dispatchEvent(new (input.ownerDocument.defaultView.Event)('change', { bubbles: true }));
      },
      remove: function (file) {
        var input = this.$refs.input;
        if (!input || !g.DataTransfer) {
          return;
        }
        var keep = this.files.filter(function (item) {
          return item !== file;
        });
        try {
          var transfer = new DataTransfer();
          keep.forEach(function (item) {
            transfer.items.add(item);
          });
          input.files = transfer.files;
        } catch (err) {
          return;
        }
        this.files = keep;
      },
      sizeText: function (bytes) {
        return sizeText(bytes);
      }
    };
  };

  // registerPhone: sanitise to 11 digits and show the 3-4-4 preview. | registerPhone：净化为 11 位并
  // 显示 3-4-4 预览。
  components.ydPhone = function () {
    return {
      value: '',
      touched: false,
      init: function () {
        this.value = digits(this.$el.getAttribute('data-value') || '');
      },
      onInput: function (event) {
        var raw = digits(event.target.value).slice(0, 11);
        if (event.target.value !== raw) {
          event.target.value = raw;
        }
        this.value = raw;
        this.touched = true;
      },
      valid: function () {
        return CN.phone(this.value);
      },
      formatted: function () {
        return CN.phoneFormatted(this.value);
      },
      state: function () {
        return !this.touched ? '' : CN.phone(this.value) ? 'ok' : 'bad';
      }
    };
  };

  // registerIDCard: uppercase X, checksum and derived birth/sex/age. | registerIDCard：大写 X、
  // 校验位与派生出生日期/性别/年龄。
  components.ydIDCard = function () {
    return {
      value: '',
      touched: false,
      init: function () {
        this.value = String(this.$el.getAttribute('data-value') || '').toUpperCase();
      },
      onInput: function (event) {
        var raw = String(event.target.value || '')
          .toUpperCase()
          .replace(/[^0-9X]/g, '')
          .slice(0, 18);
        if (event.target.value !== raw) {
          event.target.value = raw;
        }
        this.value = raw;
        this.touched = true;
      },
      valid: function () {
        return CN.idCard(this.value);
      },
      info: function () {
        return CN.idCardInfo(this.value);
      },
      birth: function () {
        var info = CN.idCardInfo(this.value);
        return info ? info.birth : '';
      },
      sex: function () {
        var info = CN.idCardInfo(this.value);
        return info ? info.sex : '';
      },
      age: function () {
        var info = CN.idCardInfo(this.value);
        return info ? info.age : '';
      },
      state: function () {
        return !this.touched ? '' : CN.idCard(this.value) ? 'ok' : 'bad';
      }
    };
  };

  // registerBankCard: digits only, Luhn and issuer lookup. | registerBankCard：仅数字、Luhn 与发卡行识别。
  components.ydBankCard = function () {
    return {
      value: '',
      touched: false,
      init: function () {
        this.value = digits(this.$el.getAttribute('data-value') || '');
      },
      onInput: function (event) {
        var raw = digits(event.target.value).slice(0, 19);
        if (event.target.value !== raw) {
          event.target.value = raw;
        }
        this.value = raw;
        this.touched = true;
      },
      valid: function () {
        return CN.bankCard(this.value);
      },
      bank: function () {
        return CN.bankName(this.value);
      },
      formatted: function () {
        return CN.bankCardFormatted(this.value);
      },
      state: function () {
        return !this.touched ? '' : CN.bankCard(this.value) ? 'ok' : 'bad';
      }
    };
  };

  // registerCreditCode: uppercase and the GB 32100 checksum. | registerCreditCode：大写与 GB 32100 校验。
  components.ydCreditCode = function () {
    return {
      value: '',
      touched: false,
      init: function () {
        this.value = String(this.$el.getAttribute('data-value') || '').toUpperCase();
      },
      onInput: function (event) {
        var raw = String(event.target.value || '')
          .toUpperCase()
          .replace(/[^0-9A-HJ-NPQRTUWXY]/g, '')
          .slice(0, 18);
        if (event.target.value !== raw) {
          event.target.value = raw;
        }
        this.value = raw;
        this.touched = true;
      },
      valid: function () {
        return CN.creditCode(this.value);
      },
      state: function () {
        return !this.touched ? '' : CN.creditCode(this.value) ? 'ok' : 'bad';
      }
    };
  };

  // registerAmount: sanitise to a non-negative decimal and render the capitalised form. | registerAmount：
  // 净化为非负小数并渲染中文大写。
  components.ydAmount = function () {
    return {
      value: '',
      touched: false,
      init: function () {
        this.value = CN.sanitizeAmount(this.$el.getAttribute('data-value') || '');
      },
      onInput: function (event) {
        var raw = CN.sanitizeAmount(event.target.value);
        if (event.target.value !== raw) {
          event.target.value = raw;
        }
        this.value = raw;
        this.touched = true;
      },
      upper: function () {
        return CN.amountToChinese(this.value);
      },
      valid: function () {
        return /^\d+(?:\.\d{0,2})?$/.test(this.value) && this.value !== '';
      },
      state: function () {
        return !this.touched ? '' : CN.amountToChinese(this.value) ? 'ok' : 'bad';
      }
    };
  };

  // registerRegion: a three-level cascader. Data comes from a same-origin JSON script when the
  // application provides one, and otherwise from the built-in province list. The htmx path for
  // remote data is documented rather than implemented here: fetching in Alpine would duplicate
  // the one mechanism htmx already owns.
  // registerRegion：三级级联。应用提供同源 JSON 脚本时用该数据，否则用内置省级列表。远程数据走
  // htmx 的做法只写进文档而不在这里实现：在 Alpine 里取数会与 htmx 已有的唯一机制重复。
  components.ydRegion = function () {
    return {
      data: [],
      provinces: [],
      cities: [],
      districts: [],
      province: '',
      city: '',
      district: '',
      init: function () {
        var source = this.$el.getAttribute('data-yd-region-source') || '';
        if (source) {
          var node = (this.$root || this.$el).ownerDocument.querySelector(source);
          if (node) {
            try {
              var parsed = JSON.parse(node.textContent || '[]');
              if (Array.isArray(parsed) && parsed.length) {
                this.data = parsed;
              }
            } catch (err) {
              this.data = [];
            }
          }
        }
        if (!this.data.length) {
          this.data = CN.provinces;
        }
        this.provinces = this.data;
        this.province = this.$el.getAttribute('data-province') || '';
        this.city = this.$el.getAttribute('data-city') || '';
        this.district = this.$el.getAttribute('data-district') || '';
        this.syncChildren();
      },
      find: function (list, code) {
        for (var i = 0; i < list.length; i++) {
          if (list[i].code === code) {
            return list[i];
          }
        }
        return null;
      },
      syncChildren: function () {
        var p = this.find(this.provinces, this.province);
        this.cities = p && p.children ? p.children : [];
        var c = this.find(this.cities, this.city);
        this.districts = c && c.children ? c.children : [];
        if (!this.find(this.cities, this.city)) {
          this.city = '';
          this.districts = [];
        }
        if (!this.find(this.districts, this.district)) {
          this.district = '';
        }
      },
      onProvince: function () {
        this.city = '';
        this.district = '';
        this.syncChildren();
        this.$dispatch('yd:region:change', { province: this.province, city: '', district: '' });
      },
      onCity: function () {
        this.district = '';
        this.syncChildren();
        this.$dispatch('yd:region:change', { province: this.province, city: this.city, district: '' });
      },
      onDistrict: function () {
        this.$dispatch('yd:region:change', {
          province: this.province,
          city: this.city,
          district: this.district
        });
      },
      complete: function () {
        return !!(this.province && this.city && this.district);
      }
    };
  };

  // registerDateRange: two native date fields plus the five presets Chinese admin screens use.
  // The presets compute local calendar days; no time zone conversion is attempted because the
  // server owns the authoritative date semantics.
  // registerDateRange：两个原生日期字段，加上中文管理后台常用的五个预设。预设按本地日历日计算；
  // 不做时区换算，因为权威的日期语义属于服务端。
  components.ydDateRange = function () {
    return {
      from: '',
      to: '',
      preset: '',
      init: function () {
        var root = this.$root || this.$el;
        this.from = this.read('from') || root.getAttribute('data-from') || '';
        this.to = this.read('to') || root.getAttribute('data-to') || '';
        this.write('from', this.from, false);
        this.write('to', this.to, false);
        var self = this;
        ['from', 'to'].forEach(function (name) {
          var input = self.field(name);
          if (input && typeof input.addEventListener === 'function') {
            input.addEventListener('input', function () {
              self.manual();
            });
          }
        });
      },
      field: function (name) {
        var root = this.$root || this.$el;
        return root.querySelector('[name="' + name + '"]');
      },
      read: function (name) {
        var input = this.field(name);
        return input ? String(input.value || '') : '';
      },
      write: function (name, value, notify) {
        var input = this.field(name);
        if (!input) {
          return;
        }
        input.value = value;
        if (notify === false) {
          return;
        }
        var doc = (this.$root || this.$el).ownerDocument;
        if (doc && typeof doc.createEvent === 'function') {
          var event = doc.createEvent('Event');
          event.initEvent('input', true, true);
          input.dispatchEvent(event);
        }
      },
      apply: function (name) {
        var now = today();
        var start;
        var end;
        if (name === 'today') {
          start = now;
          end = now;
        } else if (name === '7d') {
          start = addDays(now, -6);
          end = now;
        } else if (name === '30d') {
          start = addDays(now, -29);
          end = now;
        } else if (name === 'month') {
          start = new Date(now.getFullYear(), now.getMonth(), 1);
          end = now;
        } else if (name === 'last-month') {
          start = new Date(now.getFullYear(), now.getMonth() - 1, 1);
          end = new Date(now.getFullYear(), now.getMonth(), 0);
        } else {
          return;
        }
        this.from = ymd(start);
        this.to = ymd(end);
        this.write('from', this.from);
        this.write('to', this.to);
        this.preset = name;
        this.$dispatch('yd:daterange:change', { from: this.from, to: this.to, preset: name });
      },
      manual: function () {
        this.from = this.read('from');
        this.to = this.read('to');
        this.preset = '';
        this.$dispatch('yd:daterange:change', { from: this.from, to: this.to, preset: '' });
      },
      valid: function () {
        if (!this.from || !this.to) {
          return true;
        }
        return this.from <= this.to;
      },
      state: function () {
        return this.valid() ? '' : 'bad';
      },
      within: function (name) {
        return this.preset === name;
      }
    };
  };

  // registerSmsCode: a 60-second resend countdown around a button that htmx drives. The component
  // listens for htmx's own after-request event and only starts when the request succeeded, so a
  // 422 or a network failure never burns the countdown.
  // registerSmsCode：围绕由 htmx 驱动的按钮做 60 秒重发倒计时。组件监听 htmx 自己的请求结束事件，
  // 只在请求成功时启动，因此 422 或网络失败绝不会白白烧掉倒计时。
  components.ydSmsCode = function () {
    return {
      seconds: 0,
      timer: 0,
      endAt: 0,
      init: function () {
        var self = this;
        this._listener = function (event) {
          // htmx 4 delivers the request context, not a "successful" flag. Start the countdown only
          // for a 2xx response; an unreadable status leaves it untouched so the user can retry.
          // htmx 4 给出的是请求上下文，而不是 successful 标志。只有 2xx 响应才启动倒计时；
          // 读不出状态码时保持不动，让用户可以重试。
          var ctx = event && event.detail ? event.detail.ctx : null;
          var status = 0;
          if (ctx) {
            if (ctx.response && typeof ctx.response.status === 'number') {
              status = ctx.response.status;
            } else if (ctx.xhr && typeof ctx.xhr.status === 'number') {
              status = ctx.xhr.status;
            }
          }
          if ((status >= 200 && status < 300) || (status === 0 && event && event.detail && event.detail.successful === true)) {
            self.start();
          }
        };
        this.$el.addEventListener('htmx:after:request', this._listener);
      },
      destroy: function () {
        if (this.timer) {
          clearInterval(this.timer);
        }
        if (this._listener) {
          this.$el.removeEventListener('htmx:after:request', this._listener);
        }
      },
      start: function () {
        if (this.seconds > 0) {
          return;
        }
        var self = this;
        this.seconds = 60;
        this.endAt = Date.now() + 60000;
        this.timer = setInterval(function () {
          var remaining = Math.max(0, Math.ceil((self.endAt - Date.now()) / 1000));
          self.seconds = remaining;
          if (remaining <= 0) {
            clearInterval(self.timer);
            self.timer = 0;
          }
        }, 250);
      },
      busy: function () {
        return this.seconds > 0;
      },
      label: function () {
        return this.seconds > 0 ? this.seconds + 's' : '';
      }
    };
  };

  // registerCarousel: scroll-snap is the mechanism; the component drives the arrows, the dots and
  // the scroll position. A manual swipe updates the active dot, and ArrowLeft/ArrowRight work while
  // the carousel has focus. | registerCarousel：滚动吸附是机制；组件驱动箭头、圆点与滚动位置。
  // 手动滑动会更新当前圆点，轮播获得焦点时 ArrowLeft/ArrowRight 可用。
  components.ydCarousel = function () {
    return {
      active: 0,
      count: 0,
      init: function () {
        var self = this;
        this.count = parseInt(this.$el.getAttribute('data-count') || '0', 10) || 0;
        this._onScroll = function () {
          var track = self.$refs.track;
          if (!track || !track.children || !track.children.length) {
            return;
          }
          var best = 0;
          var distance = Infinity;
          var trackLeft = track.getBoundingClientRect().left;
          for (var i = 0; i < track.children.length; i++) {
            var childLeft = track.children[i].getBoundingClientRect().left - trackLeft + track.scrollLeft;
            var delta = Math.abs(childLeft - track.scrollLeft);
            if (delta < distance) {
              distance = delta;
              best = i;
            }
          }
          self.active = best;
        };
        this._track = this.$refs.track;
        if (this._track) {
          this._track.addEventListener('scroll', this._onScroll, { passive: true });
        }
        this.go(0, false);
      },
      destroy: function () {
        if (this._track && this._onScroll) {
          this._track.removeEventListener('scroll', this._onScroll);
        }
        this._track = null;
      },
      go: function (index, scroll) {
        if (!this.count) {
          return;
        }
        this.active = ((index % this.count) + this.count) % this.count;
        if (scroll === false) {
          return;
        }
        var track = this.$refs.track;
        var child = track && track.children ? track.children[this.active] : null;
        if (child && typeof child.scrollIntoView === 'function') {
          var reduce = false;
          try {
            var view = (this.$root || this.$el).ownerDocument.defaultView;
            reduce = !!(view.matchMedia && view.matchMedia('(prefers-reduced-motion: reduce)').matches);
          } catch (err) {
            reduce = false;
          }
          child.scrollIntoView({ behavior: reduce ? 'auto' : 'smooth', block: 'nearest', inline: 'start' });
        }
      },
      next: function () {
        this.go(this.active + 1);
      },
      prev: function () {
        this.go(this.active - 1);
      },
      onKey: function (event) {
        var key = event && event.key;
        if (key === 'ArrowLeft') {
          event.preventDefault();
          this.prev();
        } else if (key === 'ArrowRight') {
          event.preventDefault();
          this.next();
        } else if (key === 'Home') {
          event.preventDefault();
          this.go(0);
        } else if (key === 'End') {
          event.preventDefault();
          this.go(this.count - 1);
        }
      }
    };
  };

  // registerSlider: mirror the native range value for the live output. | registerSlider：把原生
  // range 的值镜像到实时输出。
  components.ydSlider = function () {
    return {
      value: '',
      init: function () {
        var input = (this.$root || this.$el).querySelector('input[type="range"]');
        this.value = input ? input.value : this.$el.getAttribute('data-value') || '';
      }
    };
  };

  // registerWatermark: repeat a label across a rotated layer. | registerWatermark：把标签重复铺满
  // 旋转后的水印层。
  components.ydWatermark = function () {
    return {
      text: '',
      count: 0,
      init: function () {
        this.text = this.$el.getAttribute('data-text') || '';
        var parsed = parseInt(this.$el.getAttribute('data-count') || '48', 10);
        this.count = parsed > 0 ? Math.min(parsed, 400) : 48;
      },
      items: function () {
        var out = [];
        for (var i = 0; i < this.count; i++) {
          out.push(i);
        }
        return out;
      }
    };
  };

  // registerAnchor: a scrollspy over same-page links. The scroll handler is coalesced through
  // requestAnimationFrame so a long page with many anchors does not run layout reads on every
  // scroll event. | registerAnchor：同页链接的滚动高亮。滚动处理器经 requestAnimationFrame 合并，
  // 因此锚点很多的长期页面不会在每次滚动事件里都做布局读取。
  components.ydAnchor = function () {
    return {
      active: '',
      init: function () {
        var self = this;
        var links = (this.$root || this.$el).querySelectorAll('a[href^="#"]');
        this._links = Array.prototype.slice.call(links);
        this._ticking = false;
        this._onScroll = function () {
          if (self._ticking) {
            return;
          }
          self._ticking = true;
          var view = (self.$root || self.$el).ownerDocument.defaultView;
          view.requestAnimationFrame(function () {
            self._ticking = false;
            var best = '';
            for (var i = 0; i < self._links.length; i++) {
              var href = self._links[i].getAttribute('href') || '';
              var target = href.length > 1 ? (self.$root || self.$el).ownerDocument.getElementById(href.slice(1)) : null;
              if (target && target.getBoundingClientRect().top <= 140) {
                best = href;
              }
            }
            self.active = best;
          });
        };
        (this.$root || this.$el).ownerDocument.defaultView.addEventListener('scroll', this._onScroll, { passive: true });
        this._onScroll();
      },
      destroy: function () {
        if (this._onScroll) {
          (this.$root || this.$el).ownerDocument.defaultView.removeEventListener('scroll', this._onScroll);
        }
      },
      isActive: function (href) {
        return this.active === href;
      }
    };
  };

  // registerBackTop: reveal the button once the page has scrolled. | registerBackTop：页面滚动后
  // 显示按钮。
  components.ydBackTop = function () {
    return {
      visible: false,
      init: function () {
        var self = this;
        this._ticking = false;
        this._onScroll = function () {
          if (self._ticking) {
            return;
          }
          self._ticking = true;
          var view = (self.$root || self.$el).ownerDocument.defaultView;
          view.requestAnimationFrame(function () {
            self._ticking = false;
            var top = view.scrollY || (self.$root || self.$el).ownerDocument.documentElement.scrollTop || 0;
            self.visible = top > 320;
          });
        };
        (this.$root || this.$el).ownerDocument.defaultView.addEventListener('scroll', this._onScroll, { passive: true });
        this._onScroll();
      },
      destroy: function () {
        if (this._onScroll) {
          (this.$root || this.$el).ownerDocument.defaultView.removeEventListener('scroll', this._onScroll);
        }
      },
      top: function () {
        var view = (this.$root || this.$el).ownerDocument.defaultView;
        var reduce = false;
        try {
          reduce = !!(view.matchMedia && view.matchMedia('(prefers-reduced-motion: reduce)').matches);
        } catch (err) {
          reduce = false;
        }
        view.scrollTo({ top: 0, behavior: reduce ? 'auto' : 'smooth' });
      }
    };
  };

  // registerCommand: a filtered command palette with a real focus trap and roving selection. The
  // input keeps focus while ArrowUp/ArrowDown move the highlight, Enter activates it and Tab cannot
  // walk out of the modal. | registerCommand：带真正焦点陷阱与漫游选中的可过滤命令面板。
  // 输入框保持焦点，ArrowUp/ArrowDown 移动高亮，Enter 激活，Tab 无法走出模态。
  components.ydCommand = function () {
    return {
      open: false,
      query: '',
      showEmpty: false,
      active: -1,
      activeId: '',
      init: function () {
        var self = this;
        var raw = String(this.$el.getAttribute('data-shortcut') || '').trim();
        var code = parseInt(raw, 10);
        // Accept both the documented single letter and the legacy numeric key code. | 同时接受文档约定的
        // 单个字母与旧的数字键码。
        this._key = raw.length === 1 && isNaN(code) ? raw.toLowerCase()
          : (code ? String.fromCharCode(code).toLowerCase() : 'k');
        this._onKey = function (event) {
          var key = String(event.key || '').toLowerCase();
          if ((event.metaKey || event.ctrlKey) && key === self._key) {
            event.preventDefault();
            self.show();
          }
        };
        this._onEscape = function (event) {
          if (event.key === 'Escape' && self.open) {
            self.close(true);
          }
        };
        (this.$root || this.$el).ownerDocument.addEventListener('keydown', this._onKey);
        (this.$root || this.$el).ownerDocument.addEventListener('keydown', this._onEscape);
      },
      destroy: function () {
        if (this._onKey) {
          (this.$root || this.$el).ownerDocument.removeEventListener('keydown', this._onKey);
        }
        if (this._onEscape) {
          (this.$root || this.$el).ownerDocument.removeEventListener('keydown', this._onEscape);
        }
      },
      show: function () {
        var self = this;
        this.open = true;
        this.query = '';
        this.showEmpty = false;
        this.active = -1;
        this.$nextTick(function () {
          if (self.$refs.input) {
            self.$refs.input.value = '';
            self.$refs.input.focus();
          }
          self.syncActive(0);
        });
      },
      close: function (restore) {
        this.open = false;
        this.active = -1;
        this.activeId = '';
        if (restore && this.$refs.trigger && typeof this.$refs.trigger.focus === 'function') {
          this.$refs.trigger.focus();
        }
      },
      matches: function (text) {
        var query = String(this.query || '').trim().toLowerCase();
        return !query || String(text || '').toLowerCase().indexOf(query) >= 0;
      },
      items: function () {
        return Array.prototype.slice.call((this.$root || this.$el).querySelectorAll('[data-yd-command-item]'));
      },
      visibleItems: function () {
        return this.items().filter(function (item) {
          return !item.hidden && item.style.display !== 'none';
        });
      },
      syncActive: function (index) {
        var items = this.visibleItems();
        this.items().forEach(function (item) {
          item.classList.remove('yd-command__item--active');
          item.setAttribute('aria-selected', 'false');
        });
        if (!items.length) {
          this.active = -1;
          this.activeId = '';
          return;
        }
        var wanted = typeof index === 'number' ? index : this.active;
        if (wanted < 0 || wanted >= items.length) {
          wanted = 0;
        }
        this.active = wanted;
        this.activeId = items[wanted].id || '';
        items[wanted].classList.add('yd-command__item--active');
        items[wanted].setAttribute('aria-selected', 'true');
      },
      move: function (step) {
        var items = this.visibleItems();
        if (!items.length) {
          return;
        }
        var start = this.active < 0 ? (step > 0 ? -1 : 0) : this.active;
        this.syncActive(((start + step) % items.length + items.length) % items.length);
      },
      choose: function () {
        var items = this.visibleItems();
        if (!items.length || this.active < 0 || this.active >= items.length) {
          return;
        }
        var target = items[this.active];
        this.close(false);
        if (typeof target.click === 'function') {
          target.click();
        }
      },
      onQuery: function (event) {
        var self = this;
        this.query = event && event.target ? event.target.value : '';
        var found = this.items().some(function (item) {
          return self.matches(item.textContent);
        });
        this.showEmpty = this.query.length > 0 && !found;
        // x-show applies the display change in the same reactive flush; wait one tick before choosing
        // the first visible item. | x-show 会在同一次响应式刷新里修改 display；等一拍再选出第一个可见项。
        this.$nextTick(function () {
          self.syncActive(0);
        });
      },
      onKey: function (event) {
        var key = event && event.key;
        if (key === 'Escape' || key === 'Esc') {
          event.preventDefault();
          this.close(true);
          return;
        }
        if (key === 'ArrowDown' || key === 'Down') {
          event.preventDefault();
          this.move(1);
          return;
        }
        if (key === 'ArrowUp' || key === 'Up') {
          event.preventDefault();
          this.move(-1);
          return;
        }
        if (key === 'Home') {
          event.preventDefault();
          this.syncActive(0);
          return;
        }
        if (key === 'End') {
          event.preventDefault();
          this.syncActive(Math.max(0, this.visibleItems().length - 1));
          return;
        }
        if (key === 'Enter') {
          event.preventDefault();
          this.choose();
          return;
        }
        if (key !== 'Tab') {
          return;
        }
        var panel = (this.$root || this.$el).querySelector('.yd-command__panel');
        if (!panel) {
          return;
        }
        var focusables = Array.prototype.slice.call(panel.querySelectorAll('input, a[href], button')).filter(function (node) {
          return !node.hidden && node.style.display !== 'none' && !node.disabled;
        });
        if (!focusables.length) {
          return;
        }
        var first = focusables[0];
        var last = focusables[focusables.length - 1];
        var doc = (this.$root || this.$el).ownerDocument;
        if (event.shiftKey && doc.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && doc.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }
    };
  };

  // registerContextMenu: position a fixed panel at the pointer (or at the focused element for a
  // keyboard invocation), trap Tab inside it, and close it on Escape, outside click or scroll.
  // registerContextMenu：把固定面板定位到指针处（键盘触发时定位到焦点元素），把 Tab 限制在面板内，
  // 并在 Escape、外部点击或滚动时关闭。
  components.ydContextMenu = function () {
    return {
      open: false,
      init: function () {
        var self = this;
        this._outside = function (event) {
          if (self.open && self.$refs.panel && !self.$refs.panel.contains(event.target)) {
            self.close(false);
          }
        };
        (this.$root || this.$el).ownerDocument.addEventListener('click', this._outside);
        (this.$root || this.$el).ownerDocument.defaultView.addEventListener('scroll', this._outside, true);
      },
      destroy: function () {
        if (this._outside) {
          (this.$root || this.$el).ownerDocument.removeEventListener('click', this._outside);
          (this.$root || this.$el).ownerDocument.defaultView.removeEventListener('scroll', this._outside, true);
        }
      },
      items: function () {
        return Array.prototype.slice.call((this.$root || this.$el).querySelectorAll('.yd-context-menu [role="menuitem"]'));
      },
      focusItem: function (index) {
        var self = this;
        this.$nextTick(function () {
          var items = self.items();
          if (!items.length) {
            return;
          }
          var picked = ((index % items.length) + items.length) % items.length;
          items[picked].focus();
        });
      },
      openAt: function (event) {
        var self = this;
        this._opener = (this.$root || this.$el).ownerDocument.activeElement;
        this.open = true;
        this.$nextTick(function () {
          var panel = self.$refs.panel;
          if (!panel) {
            return;
          }
          var view = (self.$root || self.$el).ownerDocument.defaultView;
          var width = panel.offsetWidth || 192;
          var height = panel.offsetHeight || 0;
          var keyboard = !event || (event.clientX === 0 && event.clientY === 0);
          var rect = keyboard && event.target && event.target.getBoundingClientRect
            ? event.target.getBoundingClientRect()
            : null;
          var x = rect ? rect.left : event.clientX;
          var y = rect ? rect.bottom : event.clientY;
          if (x + width > view.innerWidth - 8) {
            x = view.innerWidth - width - 8;
          }
          if (y + height > view.innerHeight - 8) {
            y = view.innerHeight - height - 8;
          }
          panel.style.left = Math.max(8, x) + 'px';
          panel.style.top = Math.max(8, y) + 'px';
          self.focusItem(0);
        });
      },
      close: function (restore) {
        var opener = this._opener;
        this.open = false;
        this._opener = null;
        if (restore && opener && typeof opener.focus === 'function') {
          opener.focus();
        }
      },
      onKey: function (event) {
        var key = event && event.key;
        if (key === 'ContextMenu' || (event && event.shiftKey && key === 'F10')) {
          event.preventDefault();
          this.openAt(event);
          return;
        }
        if (!this.open) {
          return;
        }
        if (key === 'Escape' || key === 'Esc') {
          event.preventDefault();
          this.close(true);
          return;
        }
        if (key === 'ArrowDown' || key === 'Down') {
          event.preventDefault();
          this.move(1);
          return;
        }
        if (key === 'ArrowUp' || key === 'Up') {
          event.preventDefault();
          this.move(-1);
          return;
        }
        if (key === 'Tab') {
          var panel = this.$refs.panel;
          if (!panel) {
            return;
          }
          var focusables = Array.prototype.slice.call(panel.querySelectorAll('a[href], button')).filter(function (node) {
            return !node.disabled;
          });
          if (!focusables.length) {
            return;
          }
          var first = focusables[0];
          var last = focusables[focusables.length - 1];
          var doc = (this.$root || this.$el).ownerDocument;
          if (event.shiftKey && doc.activeElement === first) {
            event.preventDefault();
            last.focus();
          } else if (!event.shiftKey && doc.activeElement === last) {
            event.preventDefault();
            first.focus();
          }
        }
      },
      move: function (step) {
        var items = this.items();
        var doc = (this.$root || this.$el).ownerDocument;
        var current = items.indexOf(doc.activeElement);
        if (current < 0) {
          current = step > 0 ? -1 : 0;
        }
        this.focusItem(current + step);
      }
    };
  };

  // registerCountdown: a deadline countdown that formats itself. The remaining time is derived from
  // Date.now() rather than from a tick counter, so a background tab or a suspended laptop cannot
  // make the countdown drift. | registerCountdown：自动格式化的截止时间倒计时。剩余时间由
  // Date.now() 推导而不是累加 tick，因此后台标签页或休眠的笔记本都不会让倒计时漂移。
  components.ydCountdown = function () {
    return {
      seconds: 0,
      timer: 0,
      endAt: 0,
      init: function () {
        var parsed = parseInt(this.$el.getAttribute('data-seconds') || '0', 10);
        this.seconds = parsed > 0 ? parsed : 0;
        if (this.$el.getAttribute('data-auto') !== 'false' && this.seconds > 0) {
          this.start();
        }
      },
      destroy: function () {
        if (this.timer) {
          clearInterval(this.timer);
        }
      },
      start: function () {
        if (this.timer || this.seconds <= 0) {
          return;
        }
        var self = this;
        this.endAt = Date.now() + this.seconds * 1000;
        this.timer = setInterval(function () {
          self.tick();
        }, 250);
        this.tick();
      },
      tick: function () {
        var remaining = Math.max(0, Math.ceil((this.endAt - Date.now()) / 1000));
        this.seconds = remaining;
        if (remaining <= 0) {
          clearInterval(this.timer);
          this.timer = 0;
          this.$dispatch('yd:countdown:done');
        }
      },
      stop: function () {
        if (this.timer) {
          clearInterval(this.timer);
          this.timer = 0;
        }
      },
      label: function () {
        var total = Math.max(0, this.seconds);
        var hours = Math.floor(total / 3600);
        var minutes = Math.floor((total % 3600) / 60);
        var secs = total % 60;
        var pad = function (n) {
          return (n < 10 ? '0' : '') + n;
        };
        return hours > 0 ? hours + ':' + pad(minutes) + ':' + pad(secs) : pad(minutes) + ':' + pad(secs);
      }
    };
  };

  // registerNumberAnimation: count up to a target with a formatter built once. Building
  // Intl.NumberFormat inside the frame callback was the main cost on a dashboard with dozens of
  // tiles; the animation is also cancelled on destroy and skipped under reduced motion.
  // registerNumberAnimation：用一次构建的格式化器滚动到目标值。在每帧回调里新建
  // Intl.NumberFormat 是几十个指标卡同时动画时的主要开销；销毁时取消动画，并遵循减弱动效偏好。
  components.ydNumberAnimation = function () {
    return {
      display: '',
      init: function () {
        var self = this;
        var el = this.$el;
        var target = parseFloat(el.getAttribute('data-value') || '0');
        if (!isFinite(target)) {
          return;
        }
        var duration = parseInt(el.getAttribute('data-duration') || '900', 10);
        if (!isFinite(duration) || duration < 0) {
          duration = 900;
        }
        duration = Math.min(duration, 5000);
        var format = el.getAttribute('data-format') || 'number';
        var locale = el.getAttribute('data-locale') || (el.ownerDocument.documentElement.lang || 'zh-CN');
        var currency = el.getAttribute('data-currency') || 'CNY';
        var formatter = null;
        try {
          if (format === 'currency') {
            formatter = new Intl.NumberFormat(locale, { style: 'currency', currency: currency });
          } else if (format === 'percent') {
            formatter = new Intl.NumberFormat(locale, { style: 'percent' });
          } else {
            formatter = new Intl.NumberFormat(locale);
          }
        } catch (err) {
          formatter = null;
        }
        var formatValue = function (value) {
          if (!formatter) {
            return String(value);
          }
          try {
            return formatter.format(format === 'percent' ? value / 100 : value);
          } catch (err) {
            return String(value);
          }
        };
        var view = el.ownerDocument.defaultView;
        var reduce = false;
        try {
          reduce = !!(view.matchMedia && view.matchMedia('(prefers-reduced-motion: reduce)').matches);
        } catch (err) {
          reduce = false;
        }
        if (reduce || duration === 0) {
          this.display = formatValue(target);
          return;
        }
        var started = null;
        var step = function (timestamp) {
          if (started === null) {
            started = timestamp;
          }
          var progress = Math.min(1, (timestamp - started) / duration);
          self.display = formatValue(target * progress);
          if (progress < 1) {
            self._raf = view.requestAnimationFrame(step);
          } else {
            self.display = formatValue(target);
            self._raf = 0;
          }
        };
        this._raf = view.requestAnimationFrame(step);
      },
      destroy: function () {
        if (this._raf) {
          var view = this.$el.ownerDocument.defaultView;
          view.cancelAnimationFrame(this._raf);
          this._raf = 0;
        }
      }
    };
  };

  // registerNumber: a native number input with stepper buttons. | registerNumber：带步进按钮的原生
  // 数字输入。
  components.ydNumber = function () {
    return {
      value: '',
      init: function () {
        var input = (this.$root || this.$el).querySelector('input[type="number"]');
        this.value = input ? input.value : '';
      },
      step: function (delta) {
        var input = (this.$root || this.$el).querySelector('input[type="number"]');
        if (!input) {
          return;
        }
        var step = parseFloat(input.getAttribute('step') || '1');
        if (!isFinite(step) || step <= 0) {
          step = 1;
        }
        var next = (parseFloat(input.value || '0') || 0) + delta * step;
        if (input.min !== '') {
          var min = parseFloat(input.min);
          if (isFinite(min)) {
            next = Math.max(min, next);
          }
        }
        if (input.max !== '') {
          var max = parseFloat(input.max);
          if (isFinite(max)) {
            next = Math.min(max, next);
          }
        }
        // Round to the step's own precision so 0.1 + 0.2 never becomes 0.30000000000000004. |
        // 按步长自身的精度取整，避免 0.1 + 0.2 变成 0.30000000000000004。
        var decimals = (String(step).split('.')[1] || '').length;
        next = Number(next.toFixed(Math.min(decimals, 10)));
        input.value = String(next);
        this.value = input.value;
        input.dispatchEvent(new (input.ownerDocument.defaultView.Event)('input', { bubbles: true }));
      }
    };
  };

  // registerOtp: one digit per box, joined into a hidden submitted field. The initial server value
  // is painted into the boxes, and a pasted code is distributed across them instead of being
  // truncated to its last digit. | registerOtp：每格一位，汇总进隐藏的提交字段。服务端初始值会
  // 填进各格，粘贴的验证码会分配到各格，而不是只保留最后一位。
  components.ydOtp = function () {
    return {
      code: '',
      length: 6,
      init: function () {
        this.length = parseInt(this.$el.getAttribute('data-length') || '6', 10) || 6;
        this.code = String(this.$el.getAttribute('data-value') || '')
          .replace(/\D+/g, '')
          .slice(0, this.length);
        this.fill();
      },
      boxes: function () {
        return Array.prototype.slice.call((this.$root || this.$el).querySelectorAll('[data-yd-otp-input]'));
      },
      fill: function (start) {
        var boxes = this.boxes();
        var offset = start || 0;
        for (var i = 0; i < boxes.length; i++) {
          boxes[i].value = i >= offset ? (this.code.charAt(i) || '') : boxes[i].value;
        }
      },
      collect: function () {
        this.code = this.boxes()
          .map(function (box) {
            return box.value || '';
          })
          .join('')
          .slice(0, this.length);
        return this.code;
      },
      onInput: function (event, index) {
        var raw = String(event.target.value || '').replace(/\D+/g, '').slice(-1);
        event.target.value = raw;
        var boxes = this.boxes();
        if (raw && index + 1 < boxes.length) {
          boxes[index + 1].focus();
        }
        this.collect();
      },
      onPaste: function (event, index) {
        var text = '';
        if (event.clipboardData) {
          text = event.clipboardData.getData('text') || '';
        } else if (event.dataTransfer) {
          text = event.dataTransfer.getData('text') || '';
        }
        var value = String(text).replace(/\D+/g, '').slice(0, this.length - index);
        if (!value) {
          return;
        }
        event.preventDefault();
        var boxes = this.boxes();
        for (var i = 0; i < value.length && index + i < boxes.length; i++) {
          boxes[index + i].value = value.charAt(i);
        }
        var next = Math.min(index + value.length, boxes.length - 1);
        if (boxes[next]) {
          boxes[next].focus();
        }
        this.collect();
      },
      onKeydown: function (event, index) {
        var boxes = this.boxes();
        if (event.key === 'Backspace' && !event.target.value && index > 0) {
          event.preventDefault();
          boxes[index - 1].value = '';
          boxes[index - 1].focus();
          this.collect();
          return;
        }
        if (event.key === 'ArrowLeft' && index > 0) {
          event.preventDefault();
          boxes[index - 1].focus();
        }
        if (event.key === 'ArrowRight' && index + 1 < boxes.length) {
          event.preventDefault();
          boxes[index + 1].focus();
        }
      }
    };
  };

  // registerColor: keep a native colour picker and its text value in sync. | registerColor：
  // 保持原生取色器与文本取值同步。
  components.ydColor = function () {
    return {
      value: '',
      init: function () {
        var text = this.$refs.text;
        var picker = this.$refs.picker;
        this.value = String((text && text.value) || this.$el.getAttribute('data-value') || '');
        if (picker && this.value) {
          picker.value = this.value;
        }
      },
      fromPicker: function () {
        var picker = this.$refs.picker;
        var text = this.$refs.text;
        if (!picker) {
          return;
        }
        this.value = picker.value;
        if (text) {
          text.value = picker.value;
        }
      },
      fromText: function () {
        var picker = this.$refs.picker;
        var text = this.$refs.text;
        if (!text) {
          return;
        }
        var value = String(text.value || '').trim();
        this.value = value;
        if (picker && /^#[0-9a-fA-F]{6}$/.test(value)) {
          picker.value = value;
        }
      },
      valid: function () {
        return /^#[0-9a-fA-F]{6}$/.test(String(this.value || '').trim());
      }
    };
  };

  // registerCombobox: filter the server-rendered options, keep one hidden submitted value, and
  // support the full keyboard contract (ArrowUp/Down, Enter, Escape). Closing without a pick
  // restores the selected label, so the visible text can never disagree with the submitted value.
  // registerCombobox：过滤服务端渲染的选项，保留一个隐藏的提交值，并支持完整的键盘契约
  // （ArrowUp/Down、Enter、Escape）。未选择就关闭时恢复已选标签，可见文字因此绝不会与提交值不一致。
  components.ydCombobox = function () {
    return {
      open: false,
      query: '',
      showEmpty: false,
      value: '',
      label: '',
      active: -1,
      activeId: '',
      init: function () {
        this.value = this.$el.getAttribute('data-value') || '';
        this.label = this.$el.getAttribute('data-label') || '';
        var text = this.$refs.text;
        if (text) {
          text.value = this.label;
        }
        this._options = Array.prototype.slice.call(
          (this.$root || this.$el).querySelectorAll('[data-yd-combobox-option]')
        );
        var self = this;
        this._outside = function (event) {
          if (self.open && !(self.$root || self.$el).contains(event.target)) {
            self.close(true);
          }
        };
        (this.$root || this.$el).ownerDocument.addEventListener('click', this._outside);
      },
      destroy: function () {
        if (this._outside) {
          (this.$root || this.$el).ownerDocument.removeEventListener('click', this._outside);
        }
        this._options = null;
      },
      show: function () {
        this.open = true;
        var text = this.$refs.text;
        if (text) {
          text.focus();
        }
        var options = this.options();
        var selected = -1;
        for (var i = 0; i < options.length; i++) {
          if (options[i].getAttribute('data-value') === this.value) {
            selected = i;
            break;
          }
        }
        this.syncActive(selected >= 0 ? selected : 0);
      },
      options: function () {
        return this._options || [];
      },
      visibleOptions: function () {
        return this.options().filter(function (option) {
          return option.style.display !== 'none';
        });
      },
      syncActive: function (index) {
        var options = this.visibleOptions();
        this.options().forEach(function (option) {
          option.classList.remove('yd-combobox__option--active');
          option.setAttribute('aria-selected', 'false');
        });
        if (!options.length) {
          this.active = -1;
          this.activeId = '';
          return;
        }
        var wanted = typeof index === 'number' ? index : this.active;
        if (wanted < 0 || wanted >= options.length) {
          wanted = 0;
        }
        this.active = wanted;
        this.activeId = options[wanted].id || '';
        options[wanted].classList.add('yd-combobox__option--active');
        options[wanted].setAttribute('aria-selected', 'true');
      },
      onInput: function (event) {
        var self = this;
        this.query = event && event.target ? event.target.value : '';
        this.open = true;
        var found = this.options().some(function (option) {
          return self.matches(option.textContent);
        });
        this.showEmpty = this.query.length > 0 && !found;
        this.$nextTick(function () {
          self.syncActive(0);
        });
      },
      close: function (restore) {
        this.open = false;
        this.active = -1;
        this.activeId = '';
        if (restore) {
          var text = this.$refs.text;
          if (text) {
            text.value = this.label;
          }
          this.query = '';
          this.showEmpty = false;
        }
      },
      pick: function (element) {
        if (!element) {
          return;
        }
        this.value = element.getAttribute('data-value') || '';
        this.label = element.getAttribute('data-label') || element.textContent || '';
        var text = this.$refs.text;
        if (text) {
          text.value = this.label;
        }
        this.query = '';
        this.showEmpty = false;
        this.open = false;
        this.active = -1;
        this.activeId = '';
        this.$dispatch('yd:combobox:change', { value: this.value });
      },
      move: function (step) {
        var options = this.visibleOptions();
        if (!options.length) {
          return;
        }
        var start = this.active < 0 ? (step > 0 ? -1 : 0) : this.active;
        this.syncActive(((start + step) % options.length + options.length) % options.length);
      },
      onKeydown: function (event) {
        var key = event && event.key;
        if (key === 'Escape' || key === 'Esc') {
          event.preventDefault();
          this.close(true);
          return;
        }
        if (key === 'ArrowDown' || key === 'Down') {
          event.preventDefault();
          if (!this.open) {
            this.show();
          } else {
            this.move(1);
          }
          return;
        }
        if (key === 'ArrowUp' || key === 'Up') {
          event.preventDefault();
          if (!this.open) {
            this.show();
          } else {
            this.move(-1);
          }
          return;
        }
        if (key === 'Enter' && this.open && this.active >= 0) {
          event.preventDefault();
          var options = this.visibleOptions();
          if (options[this.active]) {
            this.pick(options[this.active]);
          }
        }
      },
      matches: function (text) {
        var query = String(this.query || '').trim().toLowerCase();
        return !query || String(text || '').toLowerCase().indexOf(query) >= 0;
      }
    };
  };

  // EDITOR_LABELS localizes the EasyMDE toolbar titles used by the generic editor path. The
  // message centre has its own richer toolbar in yd-message-editor.js; this table keeps a page that
  // only includes EasyMDE + yd-ui.js from showing English tooltips under a Chinese UI.
  // EDITOR_LABELS 本地化通用编辑器路径使用的 EasyMDE 工具栏标题。消息中心有 yd-message-editor.js
  // 里的更完整工具栏；本表保证只引入 EasyMDE + yd-ui.js 的页面不会在中文界面里显示英文提示。
  var EDITOR_LABELS = {
    'zh-CN': {
      bold: '加粗', italic: '斜体', heading: '标题', quote: '引用',
      'generic list': '无序列表', 'numbered list': '有序列表',
      'create link': '插入链接', 'insert image': '插入图片', code: '代码',
      'insert table': '插入表格', 'toggle preview': '预览',
      'toggle side by side': '并排预览', 'toggle fullscreen': '全屏',
      undo: '撤销', redo: '重做'
    },
    en: {
      bold: 'Bold', italic: 'Italic', heading: 'Heading', quote: 'Quote',
      'generic list': 'Unordered list', 'numbered list': 'Ordered list',
      'create link': 'Insert link', 'insert image': 'Insert image', code: 'Code',
      'insert table': 'Insert table', 'toggle preview': 'Preview',
      'toggle side by side': 'Side by side', 'toggle fullscreen': 'Full screen',
      undo: 'Undo', redo: 'Redo'
    }
  };

  // editorLabel returns one localized toolbar label for the document language. | editorLabel 按文档语言
  // 返回一条工具栏文案。
  function editorLabel(doc, title) {
    var lang = (doc && doc.documentElement && doc.documentElement.lang) || 'en';
    var table = lang.toLowerCase().indexOf('zh') === 0 ? EDITOR_LABELS['zh-CN'] : EDITOR_LABELS.en;
    return table[String(title || '').toLowerCase()] || '';
  }

  // EDITOR_ICONS maps every EasyMDE toolbar action to the Lucide symbol rendered by the hidden
  // palette in partials/editor-icons.html. EasyMDE's own defaults name Font Awesome classes; those
  // are stripped below, so the rendered toolbar never depends on an icon font.
  // EDITOR_ICONS 把每个 EasyMDE 工具栏动作映射到 partials/editor-icons.html 隐藏调色板里的 Lucide
  // 符号。EasyMDE 自带默认值使用 Font Awesome 类名；下面的装饰过程会移除它们，渲染后的工具栏因此
  // 绝不依赖图标字体。
  var EDITOR_ICONS = {
    bold: 'lucide-bold',
    italic: 'lucide-italic',
    heading: 'lucide-heading',
    strikethrough: 'lucide-strikethrough',
    quote: 'lucide-quote',
    'unordered-list': 'lucide-list',
    'ordered-list': 'lucide-list-ordered',
    link: 'lucide-link',
    image: 'lucide-image',
    code: 'lucide-code',
    table: 'lucide-table',
    'horizontal-rule': 'lucide-minus',
    preview: 'lucide-eye',
    'side-by-side': 'lucide-columns-2',
    fullscreen: 'lucide-maximize',
    undo: 'lucide-undo-2',
    redo: 'lucide-redo-2'
  };

  // stripFontAwesome removes the vendor's legacy icon-font class tokens while keeping semantic
  // classes such as no-disable and no-mobile. | stripFontAwesome 移除 vendor 遗留的图标字体类名，
  // 同时保留 no-disable、no-mobile 等语义类。
  function stripFontAwesome(className) {
    return String(className || '').split(/\s+/).filter(function (token) {
      return token && token !== 'fa' && token.indexOf('fa-') !== 0;
    });
  }

  // decorateEditorToolbar replaces each EasyMDE toolbar glyph with the matching Lucide <svg> from
  // the editor's hidden palette. The palette keeps the icons server-rendered (and therefore in the
  // on-demand sprite); no icon font, CDN or data URI is involved. | decorateEditorToolbar 用编辑器
  // 隐藏调色板中对应的 Lucide <svg> 替换每个 EasyMDE 工具栏字形。调色板让图标仍由服务端渲染
  // （因而仍进入按需 sprite）；不涉及图标字体、CDN 或 data URI。
  function decorateEditorToolbar(editor, root) {
    if (!editor || !editor.toolbarElements || !root || typeof root.querySelector !== 'function') {
      return;
    }
    var palette = root.querySelector('[data-yd-editor-icons]');
    if (!palette) {
      var doc = root.ownerDocument || g.document;
      palette = doc && typeof doc.querySelector === 'function' ? doc.querySelector('[data-yd-editor-icons]') : null;
    }
    if (!palette) {
      return;
    }
    Object.keys(editor.toolbarElements).forEach(function (name) {
      var button = editor.toolbarElements[name];
      var lucide = EDITOR_ICONS[name];
      if (!button || !lucide) {
        return;
      }
      var holder = palette.querySelector('[data-yd-editor-icon="' + lucide + '"]');
      var icon = holder && holder.querySelector('svg');
      var classes = stripFontAwesome(button.className);
      if (classes.indexOf('yd-editor-tool') < 0) {
        classes.unshift('yd-editor-tool');
      }
      button.className = classes.join(' ');
      if (icon) {
        button.innerHTML = '';
        button.appendChild(icon.cloneNode(true));
      }
      var title = button.getAttribute('title');
      if (title) {
        button.setAttribute('aria-label', title);
      }
    });
  }

  // registerEmoji: insert an emoji into an existing named input or textarea. The palette never
  // creates a field and never submits; it edits the control the page already rendered.
  // registerEmoji：把表情插入已存在的命名字段或文本域。面板不创建字段、不提交，只编辑页面已经渲染的控件。
  components.ydEmoji = function () {
    return {
      pick: function (emoji) {
        var root = this.$root || this.$el;
        var doc = root.ownerDocument;
        var selector = root.getAttribute('data-target');
        var target = selector && doc ? doc.querySelector(selector) : null;
        if (!target || typeof target.value !== 'string') {
          return;
        }
        var start = typeof target.selectionStart === 'number' ? target.selectionStart : target.value.length;
        var end = typeof target.selectionEnd === 'number' ? target.selectionEnd : start;
        target.value = target.value.slice(0, start) + emoji + target.value.slice(end);
        var caret = start + emoji.length;
        if (typeof target.setSelectionRange === 'function') {
          target.setSelectionRange(caret, caret);
        }
        var event = doc.createEvent('Event');
        event.initEvent('input', true, true);
        target.dispatchEvent(event);
        if (typeof target.focus === 'function') {
          target.focus();
        }
        this.$dispatch('yd:emoji:pick', { emoji: emoji });
      }
    };
  };

  // registerSignature: draw on a canvas and keep the submitted value in the hidden field. A missing
  // 2D context is treated as "no drawing", never as an exception that breaks the form.
  // registerSignature：在画布上绘制，并把提交值保存在隐藏字段里。缺少 2D 上下文时视为「不能绘制」，
  // 绝不以异常打断表单。
  components.ydSignature = function () {
    return {
      value: '',
      drawing: false,
      ctx: null,
      canvas: null,
      init: function () {
        this.canvas = this.$refs.canvas;
        if (!this.canvas || typeof this.canvas.getContext !== 'function') {
          return;
        }
        this.ctx = this.canvas.getContext('2d');
        if (!this.ctx) {
          return;
        }
        this.ctx.lineWidth = 2;
        this.ctx.lineCap = 'round';
        this.ctx.lineJoin = 'round';
        var doc = (this.$root || this.$el).ownerDocument;
        var win = doc && doc.defaultView;
        var style = win && win.getComputedStyle ? win.getComputedStyle(this.canvas) : null;
        this.ctx.strokeStyle = (style && style.color) || 'currentColor';
        if (this.$refs.value && this.$refs.value.value) {
          this.value = this.$refs.value.value;
          this.restore(this.value);
        }
      },
      restore: function (source) {
        var self = this;
        if (!this.ctx || !source) {
          return;
        }
        var img = new Image();
        img.onload = function () {
          self.ctx.drawImage(img, 0, 0, self.canvas.width, self.canvas.height);
        };
        img.src = source;
      },
      point: function (event) {
        var rect = this.canvas.getBoundingClientRect();
        var scaleX = rect.width ? this.canvas.width / rect.width : 1;
        var scaleY = rect.height ? this.canvas.height / rect.height : 1;
        return { x: (event.clientX - rect.left) * scaleX, y: (event.clientY - rect.top) * scaleY };
      },
      start: function (event) {
        if (!this.ctx) {
          return;
        }
        this.drawing = true;
        var point = this.point(event);
        this.ctx.beginPath();
        this.ctx.moveTo(point.x, point.y);
        if (event.pointerId && this.canvas.setPointerCapture) {
          this.canvas.setPointerCapture(event.pointerId);
        }
      },
      draw: function (event) {
        if (!this.drawing || !this.ctx) {
          return;
        }
        var point = this.point(event);
        this.ctx.lineTo(point.x, point.y);
        this.ctx.stroke();
      },
      end: function () {
        if (!this.drawing) {
          return;
        }
        this.drawing = false;
        if (this.canvas && this.canvas.toDataURL) {
          this.value = this.canvas.toDataURL('image/png');
        }
        this.$dispatch('yd:signature:change', { value: this.value });
      },
      clear: function () {
        if (this.ctx && this.canvas) {
          this.ctx.clearRect(0, 0, this.canvas.width, this.canvas.height);
        }
        this.value = '';
        this.$dispatch('yd:signature:change', { value: '' });
      }
    };
  };

  // registerResizable: a two-pane split with a pointer- and keyboard-driven separator. The document
  // listeners are removed on destroy so an htmx swap cannot leave a drag handler behind.
  // registerResizable：带指针与键盘分隔条的两栏布局。销毁时移除文档监听器，因此 htmx 交换不会留下
  // 拖拽处理器。
  components.ydResizable = function () {
    return {
      split: 50,
      min: 20,
      max: 80,
      orientation: 'horizontal',
      dragging: false,
      init: function () {
        var root = this.$root || this.$el;
        var parsed = parseInt(root.getAttribute('data-split'), 10);
        this.split = isFinite(parsed) ? parsed : 50;
        parsed = parseInt(root.getAttribute('data-min'), 10);
        this.min = isFinite(parsed) ? parsed : 20;
        parsed = parseInt(root.getAttribute('data-max'), 10);
        this.max = isFinite(parsed) ? parsed : 80;
        this.orientation = root.getAttribute('data-orientation') || 'horizontal';
        this.apply();
      },
      start: function () {
        var self = this;
        var doc = (this.$root || this.$el).ownerDocument;
        this.dragging = true;
        this._move = function (event) {
          self.move(event);
        };
        this._up = function () {
          self.stop();
        };
        doc.addEventListener('pointermove', this._move);
        doc.addEventListener('pointerup', this._up);
      },
      stop: function () {
        var doc = (this.$root || this.$el).ownerDocument;
        this.dragging = false;
        if (this._move) {
          doc.removeEventListener('pointermove', this._move);
          this._move = null;
        }
        if (this._up) {
          doc.removeEventListener('pointerup', this._up);
          this._up = null;
        }
      },
      destroy: function () {
        this.stop();
      },
      move: function (event) {
        if (!this.dragging) {
          return;
        }
        var root = this.$root || this.$el;
        var rect = root.getBoundingClientRect();
        var ratio = this.orientation === 'vertical'
          ? (rect.height ? (event.clientY - rect.top) / rect.height : 0.5)
          : (rect.width ? (event.clientX - rect.left) / rect.width : 0.5);
        this.set(ratio * 100);
      },
      nudge: function (delta) {
        this.set(this.split + delta);
      },
      set: function (value) {
        var next = Math.round(value);
        if (!isFinite(next)) {
          return;
        }
        if (next < this.min) {
          next = this.min;
        }
        if (next > this.max) {
          next = this.max;
        }
        this.split = next;
        this.apply();
      },
      apply: function () {
        var root = this.$root || this.$el;
        root.style.setProperty('--yd-split', this.split + '%');
        var splitter = this.$refs.splitter;
        if (splitter) {
          splitter.setAttribute('aria-valuenow', String(this.split));
        }
      }
    };
  };

  // splitLabels turns a pipe-separated catalogue value into a list, falling back to English when a
  // translation is missing. | splitLabels 把竖线分隔的目录取值拆成列表，缺失翻译时回退英文。
  function splitLabels(value, fallback) {
    var parts = String(value || '').split('|');
    var out = [];
    for (var i = 0; i < parts.length; i++) {
      var part = parts[i].trim();
      if (part) {
        out.push(part);
      }
    }
    return out.length ? out : fallback.slice();
  }

  // padRange returns zero-padded values from 00 to count-1. | padRange 返回从 00 到 count-1 的补零值。
  function padRange(count) {
    var out = [];
    for (var i = 0; i < count; i++) {
      out.push(pad2(i));
    }
    return out;
  }

  // dateParts and timeParts read the ISO-like values the picker emits. | dateParts 与 timeParts 读取
  // 选择器输出的 ISO 风格取值。
  function dateParts(value) {
    var match = /^(\d{4})-(\d{2})-(\d{2})/.exec(String(value || ''));
    if (!match) {
      return null;
    }
    return { year: parseInt(match[1], 10), month: parseInt(match[2], 10) - 1, day: parseInt(match[3], 10) };
  }

  function timeParts(value) {
    var match = /(\d{2}):(\d{2})/.exec(String(value || ''));
    if (!match) {
      return null;
    }
    return { hour: match[1], minute: match[2] };
  }

  // monthParts reads the YYYY-MM value emitted by the month picker. | monthParts 读取月份选择器
  // 输出的 YYYY-MM 取值。
  function monthParts(value) {
    var match = /^(\d{4})-(\d{2})$/.exec(String(value || ''));
    if (!match) {
      return null;
    }
    return { year: parseInt(match[1], 10), month: parseInt(match[2], 10) - 1 };
  }

  // registerDateTime: a locale-independent date/time picker. The visible field is a text input and
  // the submitted value stays ISO-like; month and weekday labels come from the server's catalogue,
  // so the popup can never mix the browser locale with the page language. |
  // registerDateTime：与区域设置无关的日期/时间选择器。可见字段是文本框，提交值保持 ISO 风格；
  // 月名与周名来自服务端目录，因此弹层绝不会把浏览器区域设置与页面语言混在一起。
  components.ydDateTime = function () {
    return {
      open: false,
      kind: 'date',
      year: 0,
      month: 0,
      day: 1,
      hour: '00',
      minute: '00',
      months: [],
      weekdays: [],
      days: [],
      hours: [],
      minutes: [],
      monthLabel: '',
      min: '',
      max: '',
      init: function () {
        var root = this.$root || this.$el;
        this.kind = root.getAttribute('data-kind') || 'date';
        this.min = root.getAttribute('data-min') || '';
        this.max = root.getAttribute('data-max') || '';
        this.months = splitLabels(root.getAttribute('data-months'), [
          'January', 'February', 'March', 'April', 'May', 'June',
          'July', 'August', 'September', 'October', 'November', 'December'
        ]);
        this.weekdays = splitLabels(root.getAttribute('data-weekdays'), [
          'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'
        ]);
        this.hours = padRange(24);
        this.minutes = padRange(60);
        var now = new Date();
        this.year = now.getFullYear();
        this.month = now.getMonth();
        this.day = now.getDate();
        this.hour = pad2(now.getHours());
        this.minute = pad2(now.getMinutes());
        this.parseInput();
      },
      parseInput: function () {
        var input = this.$refs.input;
        if (!input) {
          return;
        }
        var value = String(input.value || '').trim();
        if (this.kind === 'month') {
          var month = monthParts(value);
          if (month) {
            this.year = month.year;
            this.month = month.month;
          }
          this.build();
          return;
        }
        var date = dateParts(value);
        var time = timeParts(value);
        if (date) {
          this.year = date.year;
          this.month = date.month;
          this.day = date.day;
        }
        if (time) {
          this.hour = time.hour;
          this.minute = time.minute;
        }
        this.build();
      },
      build: function () {
        if (this.kind === 'month') {
          this.days = [];
          this.monthLabel = String(this.year);
          return;
        }
        var first = new Date(this.year, this.month, 1);
        var offset = (first.getDay() + 6) % 7;
        var count = new Date(this.year, this.month + 1, 0).getDate();
        var now = new Date();
        var cells = [];
        for (var blank = 0; blank < offset; blank++) {
          cells.push({ key: 'blank-' + blank, label: '', day: 0, className: 'yd-datetime__day--blank' });
        }
        for (var day = 1; day <= count; day++) {
          var className = '';
          var iso = this.year + '-' + pad2(this.month + 1) + '-' + pad2(day);
          var disabled = (this.min && iso < this.min) || (this.max && iso > this.max);
          if (day === this.day) {
            className = 'yd-datetime__day--selected';
          } else if (now.getFullYear() === this.year && now.getMonth() === this.month && now.getDate() === day) {
            className = 'yd-datetime__day--today';
          }
          cells.push({
            key: iso,
            label: String(day),
            day: day,
            disabled: disabled,
            className: className
          });
        }
        this.days = cells;
        this.monthLabel = (this.months[this.month] || '') + ' ' + this.year;
      },
      prevMonth: function () {
        if (this.kind === 'month') {
          this.year -= 1;
          this.build();
          return;
        }
        if (this.month === 0) {
          this.month = 11;
          this.year -= 1;
        } else {
          this.month -= 1;
        }
        this.build();
      },
      nextMonth: function () {
        if (this.kind === 'month') {
          this.year += 1;
          this.build();
          return;
        }
        if (this.month === 11) {
          this.month = 0;
          this.year += 1;
        } else {
          this.month += 1;
        }
        this.build();
      },
      monthDisabled: function (index) {
        var key = this.year + '-' + pad2(index + 1);
        return Boolean((this.min && key + '-01' < this.min && key < this.min.slice(0, 7)) || (this.max && key + '-01' > this.max && key > this.max.slice(0, 7)));
      },
      pickMonth: function (index) {
        if (index < 0 || index > 11 || this.monthDisabled(index)) {
          return;
        }
        this.month = index;
        this.day = 1;
        this.apply();
      },
      pickDay: function (cell) {
        if (!cell || !cell.day || cell.disabled) {
          return;
        }
        this.day = cell.day;
        if (this.kind === 'date') {
          this.apply();
          return;
        }
        this.build();
      },
      today: function () {
        var now = new Date();
        this.year = now.getFullYear();
        this.month = now.getMonth();
        this.day = now.getDate();
        this.hour = pad2(now.getHours());
        this.minute = pad2(now.getMinutes());
        this.apply();
      },
      apply: function () {
        var date = this.year + '-' + pad2(this.month + 1) + '-' + pad2(this.day);
        var time = this.hour + ':' + this.minute;
        var value = this.kind === 'time' ? time : this.kind === 'datetime' ? date + 'T' + time : this.kind === 'month' ? this.year + '-' + pad2(this.month + 1) : date;
        var input = this.$refs.input;
        if (input) {
          input.value = value;
          var doc = (this.$root || this.$el).ownerDocument;
          if (doc && typeof doc.createEvent === 'function') {
            var event = doc.createEvent('Event');
            event.initEvent('input', true, true);
            input.dispatchEvent(event);
          }
        }
        this.close(false);
      },
      toggle: function () {
        var input = this.$refs.input;
        if (!input || input.disabled || input.readOnly) {
          return;
        }
        this.parseInput();
        this.open = !this.open;
      },
      close: function (restore) {
        this.open = false;
        if (restore && this.$refs.input && typeof this.$refs.input.focus === 'function') {
          this.$refs.input.focus();
        }
      }
    };
  };

  // registerEditor: initialise EasyMDE when the page included it, keep the textarea as the form
  // field and re-apply the dark class when the theme changes. | registerEditor：页面引入 EasyMDE 时
  // 初始化它，保留 textarea 作为表单字段，并在主题切换时重新应用暗色类。
  components.ydEditor = function () {
    return {
      editor: null,
      init: function () {
        var root = this.$root || this.$el;
        var win = root.ownerDocument.defaultView;
        // Message pages load the dedicated yd-message-editor.js, which owns the localized toolbar
        // and compact layout. Initialising EasyMDE here as well would create two editors over one
        // textarea; defer to that owner and only keep the generic path for pages without it.
        // 消息页加载了专用的 yd-message-editor.js，它拥有本地化工具栏与紧凑布局。这里再初始化一次
        // EasyMDE 会在同一个 textarea 上创建两个编辑器；因此把所有权让给它，只有没有它的页面才走
        // 通用初始化路径。
        if (win.yd && win.yd.messageEditor && typeof win.yd.messageEditor.init === 'function') {
          win.yd.messageEditor.init(this.$el.parentNode || root);
          this._delegated = true;
          return;
        }
        var el = root.querySelector('textarea');
        if (!el || el.dataset.ydEditorReady) {
          return;
        }
        // Page scripts load after Alpine in the documented order, so the first init can run
        // before EasyMDE exists. Retry once the document finishes loading instead of leaving a
        // textarea that never upgrades. | 按文档顺序，页面脚本排在 Alpine 之后，首次 init 可能早于
        // EasyMDE 就绪。文档加载完成后再试一次，避免 textarea 永远不升级。
        if (!win.EasyMDE) {
          var self = this;
          if (typeof win.addEventListener === 'function' && !this._waitingForEditor) {
            this._waitingForEditor = true;
            win.addEventListener('load', function () {
              self._waitingForEditor = false;
              self.init();
            }, {once: true});
          }
          return;
        }
        el.dataset.ydEditorReady = '1';
        try {
          this.editor = new win.EasyMDE({
            element: el,
            spellChecker: false,
            status: false,
            // Never fetch EasyMDE's legacy icon stylesheet; localizeToolbar swaps in Lucide SVGs.
            // 绝不拉取 EasyMDE 的遗留图标样式表；localizeToolbar 会换成 Lucide SVG。
            autoDownloadFontAwesome: false,
            placeholder: el.getAttribute('placeholder') || '',
            toolbar: ['bold', 'italic', 'heading', '|', 'quote', 'unordered-list', 'ordered-list', '|', 'link', 'image', 'code', 'table', '|', 'preview', 'side-by-side', 'fullscreen']
          });
        } catch (err) {
          this.editor = null;
          return;
        }
        this.syncTheme();
        this.localizeToolbar();
        var self = this;
        this._onTheme = function () {
          self.syncTheme();
        };
        root.ownerDocument.addEventListener('yd:theme', this._onTheme);
      },
      destroy: function () {
        if (this._onTheme) {
          var root = this.$root || this.$el;
          root.ownerDocument.removeEventListener('yd:theme', this._onTheme);
        }
        if (this._delegated) {
          var win = (this.$root || this.$el).ownerDocument.defaultView;
          if (win.yd && win.yd.messageEditor && typeof win.yd.messageEditor.destroy === 'function') {
            win.yd.messageEditor.destroy(this.$el);
          }
          return;
        }
        if (this.editor) {
          this.editor.toTextArea();
          this.editor = null;
        }
        var el = (this.$root || this.$el).querySelector('textarea');
        if (el && el.dataset) {
          delete el.dataset.ydEditorReady;
        }
      },
      syncTheme: function () {
        var dark = (this.$root || this.$el).ownerDocument.documentElement.getAttribute('data-theme') === 'dark';
        (this.$root || this.$el).classList.toggle('yd-editor--dark', dark);
      },
      localizeToolbar: function () {
        var root = this.$root || this.$el;
        var doc = root.ownerDocument;
        var buttons = root.querySelectorAll('.editor-toolbar button');
        for (var i = 0; i < buttons.length; i++) {
          var raw = buttons[i].getAttribute('title') || buttons[i].getAttribute('aria-label') || '';
          var label = editorLabel(doc, raw);
          if (label) {
            buttons[i].setAttribute('title', label);
            buttons[i].setAttribute('aria-label', label);
          }
        }
        decorateEditorToolbar(this.editor, root);
      }
    };
  };

  // register: install every component on an Alpine instance. The guard makes repeated calls
  // harmless when both the immediate and the alpine:init path run. | register：把全部组件安装到
  // Alpine 实例上。守卫使「立即注册」与「alpine:init 注册」两条路径同时执行也不会重复。
  function register(Alpine) {
    if (!Alpine || typeof Alpine.data !== 'function' || Alpine.__ydUIRegistered) {
      return;
    }
    Alpine.__ydUIRegistered = true;
    Object.keys(components).forEach(function (name) {
      Alpine.data(name, components[name]);
    });
  }

  var api = {
    version: VERSION,
    components: Object.keys(components),
    register: register,
    decorateEditorToolbar: decorateEditorToolbar,
    CN: CN
  };

  // Register at load time when Alpine is already present (the documented script order), and again
  // on alpine:init for a page that loads this file before Alpine.
  // Alpine 已就位时（文档规定的脚本顺序）在加载期注册；若页面把本文件排在 Alpine 之前，
  // 则在 alpine:init 时再注册一次。
  register(g.Alpine);
  var d = g.document;
  if (d && typeof d.addEventListener === 'function') {
    d.addEventListener('alpine:init', function () {
      register(g.Alpine);
    });
  }

  return api;
});
