/*
 * yd-workflow-designer.js — the same-origin visual designer for the workflow module. |
 * yd-workflow-designer.js —— 流程模块的同源可视化设计器。
 *
 * 设计目标 | Design goals:
 *   - 不依赖任何第三方库，不出现 eval / new Function / 内联事件处理器，满足 CSP `script-src 'self'`。
 *     No third-party library, no eval / new Function / inline handler, so it runs under `script-src 'self'`.
 *   - 服务端只提供初始设计 JSON、目录 JSON 与动作地址；本脚本负责画布、属性面板、表单与自动审批规则。
 *     The server supplies the initial design JSON, the directory JSON and the action URLs; this script owns the
 *     canvas, inspector, forms and automatic-approval rules.
 *   - 保存与发布都走 JSON API，成功后只更新状态行；页面本身由服务端渲染，脚本不拼业务文案。
 *     Saving and publishing go through the JSON API and only refresh the status line; the page itself is
 *     server-rendered and this script owns no business copy.
 */
(function () {
  'use strict';

  // ---------------------------------------------------------------------------
  // 基础工具 | Small helpers
  // ---------------------------------------------------------------------------

  var SVG_NS = 'http://www.w3.org/2000/svg';
  var CANVAS_W = 2400;
  var CANVAS_H = 1600;
  var CONTROL_SEQ = 0;

  // nextControlID keeps generated labels and controls associated without colliding when the
  // inspector is rebuilt. | nextControlID 保证属性面板重建时，生成的标签与控件始终关联且不冲突。
  function nextControlID(prefix) {
    CONTROL_SEQ += 1;
    return prefix + '-' + CONTROL_SEQ;
  }

  // cloneIcon copies a server-rendered icon template into dynamically built component markup,
  // keeping the Iconify sprite as the only icon source. | cloneIcon 把服务端渲染的图标模板复制到
  // 动态构建的组件标记中，确保 Iconify sprite 仍是唯一图标来源。
  function cloneIcon(id) {
    var template = document.getElementById(id);
    return template && template.content ? template.content.cloneNode(true) : null;
  }

  // LABELS is the server-rendered catalogue island of this page: every dynamic label goes
  // through L, so the designer follows the server language without embedding business copy. |
  // LABELS 是本页由服务端渲染的目录岛：所有动态文案都经 L 取词，设计器因此跟随服务端语言，
  // 不再把业务文案写死在脚本里。
  function readLabels() {
    var node = document.getElementById('yd-workflow-labels');
    if (!node) {
      return {};
    }
    try {
      var parsed = JSON.parse(node.textContent || '{}');
      return parsed && typeof parsed === 'object' ? parsed : {};
    } catch (err) {
      return {};
    }
  }

  var LABELS = readLabels();

  // NODE_KEYS maps a node type onto its catalogue slug. | NODE_KEYS 把节点类型映射到目录键。
  var NODE_KEYS = {
    start: 'label_start',
    end: 'label_end',
    user: 'label_approval',
    service: 'label_service',
    exclusive: 'label_exclusive',
    parallel: 'label_parallel',
    inclusive: 'label_inclusive',
    catch: 'label_wait',
    event: 'label_node_event'
  };

  // L reads one catalogue entry, falling back to the English literal when the island is absent
  // (for example a cached older page) and interpolates {name} placeholders. |
  // L 读取一条目录文案；目录岛缺失时（例如缓存的旧页面）回退到英文字面量，并填充 {name} 占位符。
  function L(key, fallback, args) {
    var text = LABELS[key];
    if (typeof text !== 'string' || text === '') {
      text = fallback || key;
    }
    if (args && typeof args === 'object') {
      text = text.replace(/\{([\w.-]+)\}/g, function (match, name) {
        return Object.prototype.hasOwnProperty.call(args, name) ? String(args[name]) : match;
      });
    }
    return text;
  }

  function nodeLabel(type) {
    return NODE_KEYS[type] ? L(NODE_KEYS[type], type) : type;
  }

  /* 优先使用全局确认对话框；全局脚本不可用时退回原生确认，保证阻断语义不丢失。 |
     Prefer the global confirmation dialog; fall back to the native prompt when the global script is
     unavailable so the blocking semantics never disappear. */
  function askConfirm(message, onConfirm) {
    if (!message) {
      onConfirm();
      return;
    }
    if (window.yd && typeof window.yd.confirm === 'function') {
      window.yd.confirm(message, onConfirm);
      return;
    }
    if (window.confirm(message)) {
      onConfirm();
    }
  }

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (key) {
        var value = attrs[key];
        if (value === null || value === undefined || value === false) {
          return;
        }
        if (key === 'className') {
          node.className = value;
        } else if (key === 'text') {
          node.textContent = value;
        } else if (key.indexOf('on') === 0 && typeof value === 'function') {
          node.addEventListener(key.slice(2), value);
        } else if (value === true) {
          node.setAttribute(key, '');
        } else {
          node.setAttribute(key, String(value));
        }
      });
    }
    (children || []).forEach(function (child) {
      if (child === null || child === undefined || child === false) {
        return;
      }
      node.appendChild(typeof child === 'string' ? document.createTextNode(child) : child);
    });
    return node;
  }

  function svg(tag, attrs, children) {
    var node = document.createElementNS(SVG_NS, tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (key) {
        var value = attrs[key];
        if (value === null || value === undefined || value === false) {
          return;
        }
        node.setAttribute(key, String(value));
      });
    }
    (children || []).forEach(function (child) {
      if (child) {
        node.appendChild(child);
      }
    });
    return node;
  }

  function clone(value) {
    return JSON.parse(JSON.stringify(value));
  }

  function readJSON(selector) {
    var holder = document.querySelector(selector);
    if (!holder) {
      return null;
    }
    var text = (holder.textContent || '').trim();
    if (!text) {
      return null;
    }
    var parsed = JSON.parse(text);
    // Go templates may quote a string inserted into a script context; accept both shapes. |
    // Go 模板可能把字符串放进脚本上下文并再次加引号，两种形态都接受。
    if (typeof parsed === 'string') {
      parsed = JSON.parse(parsed);
    }
    return parsed;
  }

  function nextID(prefix, taken) {
    var index = 1;
    while (taken[prefix + index]) {
      index += 1;
    }
    return prefix + index;
  }

  function truncate(text, max) {
    text = String(text || '');
    if (text.length <= max) {
      return text;
    }
    return text.slice(0, max - 1) + '…';
  }

  function parseOptions(text) {
    return String(text || '')
      .split('\n')
      .map(function (line) {
        return line.trim();
      })
      .filter(function (line) {
        return line !== '';
      })
      .map(function (line) {
        var parts = line.split('|');
        return {
          value: (parts[0] || '').trim(),
          label: (parts[1] || parts[0] || '').trim()
        };
      })
      .filter(function (option) {
        return option.value !== '';
      });
  }

  function optionsText(options) {
    return (options || [])
      .map(function (option) {
        return option.value + '|' + (option.label || option.value);
      })
      .join('\n');
  }

  // ---------------------------------------------------------------------------
  // 入口 | Entry point
  // ---------------------------------------------------------------------------

  /* 活动设计器实例：window 级的 beforeunload 只注册一次，避免每次 htmx 外壳交换都叠加监听器。
     Detached roots are pruned on every boot, so a language switch or partial navigation cannot leave
     stale dirty state behind. | Active designer instances: the window-level beforeunload listener is
     registered once, so an htmx shell swap cannot stack listeners; detached roots are pruned on boot. */
  var activeDesigners = [];

  function pruneDesigners() {
    var live = [];
    activeDesigners.forEach(function (entry) {
      if (entry.root && document.documentElement.contains(entry.root)) {
        live.push(entry);
      }
    });
    activeDesigners = live;
  }

  window.addEventListener('beforeunload', function (event) {
    var dirty = false;
    activeDesigners.forEach(function (entry) {
      if (entry.state && entry.state.dirty) {
        dirty = true;
      }
    });
    if (!dirty) {
      return;
    }
    event.preventDefault();
    event.returnValue = '';
  });

  function createDesigner(root) {
    var design = readJSON('#yd-workflow-design') || {};
    var directory = readJSON('#yd-workflow-directory') || { users: [], roles: [], groups: [] };
    design.nodes = design.nodes || [];
    design.edges = design.edges || [];
    design.forms = design.forms || {};
    design.rules = design.rules || [];
    design.settings = design.settings || {};
    design.schema_version = design.schema_version || 1;

    var canvas = root.querySelector('[data-yd-flow-canvas]');
    var stage = root.querySelector('[data-yd-flow-stage]');
    var layers = root.querySelector('[data-yd-flow-layers]');
    var inspector = root.querySelector('[data-yd-flow-inspector]');
    var statusLine = root.querySelector('[data-yd-flow-status]');

    var state = {
      zoom: 1,
      selected: null, // {kind:'node'|'edge', id:string}
      connect: false,
      connectFrom: null,
      drag: null,
      note: '',
      dirty: false
    };

    if (!stage || !layers || !inspector) {
      return;
    }
    activeDesigners.push({ root: root, state: state });
    stage.setAttribute('width', String(CANVAS_W));
    stage.setAttribute('height', String(CANVAS_H));
    stage.style.width = CANVAS_W + 'px';
    stage.style.height = CANVAS_H + 'px';
    stage.style.transformOrigin = '0 0';

    function setStatus(text, tone) {
      if (!statusLine) {
        return;
      }
      statusLine.textContent = text;
      statusLine.dataset.tone = tone || '';
    }

    function markDirty() {
      state.dirty = true;
    }

    // -----------------------------------------------------------------------
    // 画布几何 | Canvas geometry
    // -----------------------------------------------------------------------

    function boxOf(node) {
      switch (node.type) {
        case 'start':
        case 'end':
          return { w: 64, h: 64 };
        case 'exclusive':
        case 'parallel':
        case 'inclusive':
        case 'event':
          return { w: 88, h: 88 };
        case 'catch':
          return { w: 80, h: 80 };
        default:
          return { w: 176, h: 68 };
      }
    }

    function centerOf(node) {
      var box = boxOf(node);
      var x = Number(node.position && node.position.x) || 0;
      var y = Number(node.position && node.position.y) || 0;
      return { x: x + box.w / 2, y: y + box.h / 2 };
    }

    function portOf(node, toward) {
      var center = centerOf(node);
      var box = boxOf(node);
      var dx = toward.x - center.x;
      var dy = toward.y - center.y;
      if (dx === 0 && dy === 0) {
        return center;
      }
      if (node.type === 'start' || node.type === 'end' || node.type === 'catch') {
        var radius = Math.min(box.w, box.h) / 2;
        var length = Math.sqrt(dx * dx + dy * dy) || 1;
        return { x: center.x + (dx / length) * radius, y: center.y + (dy / length) * radius };
      }
      // Rectangle or diamond: clip against the half extents. | 矩形或菱形：按半宽半高求得交点。
      var halfW = box.w / 2;
      var halfH = box.h / 2;
      var scale = Math.min(
        dx === 0 ? Infinity : halfW / Math.abs(dx),
        dy === 0 ? Infinity : halfH / Math.abs(dy)
      );
      if (!isFinite(scale)) {
        scale = 0;
      }
      return { x: center.x + dx * scale, y: center.y + dy * scale };
    }

    function findNode(id) {
      for (var i = 0; i < design.nodes.length; i += 1) {
        if (design.nodes[i].id === id) {
          return design.nodes[i];
        }
      }
      return null;
    }

    function findEdge(id) {
      for (var i = 0; i < design.edges.length; i += 1) {
        if (design.edges[i].id === id) {
          return design.edges[i];
        }
      }
      return null;
    }

    function nodeIDs() {
      var out = {};
      design.nodes.forEach(function (node) {
        out[node.id] = true;
      });
      return out;
    }

    function edgeIDs() {
      var out = {};
      design.edges.forEach(function (edge) {
        out[edge.id] = true;
      });
      return out;
    }

    function isSelected(kind, id) {
      return Boolean(state.selected && state.selected.kind === kind && state.selected.id === id);
    }

    // -----------------------------------------------------------------------
    // 画布渲染 | Canvas rendering
    // -----------------------------------------------------------------------

    function drawShape(node) {
      var box = boxOf(node);
      var w = box.w;
      var h = box.h;
      var group = svg('g', {
        'class': 'yd-flow__node' + (isSelected('node', node.id) ? ' is-selected' : ''),
        'data-node-id': node.id,
        'transform': 'translate(' + (Number(node.position && node.position.x) || 0) + ',' + (Number(node.position && node.position.y) || 0) + ')',
        'tabindex': '0',
        'role': 'button',
        'aria-label': node.name || node.id
      });
      group.appendChild(svg('title', {}, [document.createTextNode(node.name || node.id)]));

      if (node.type === 'start' || node.type === 'end') {
        group.appendChild(svg('circle', {
          'class': 'yd-flow__shape yd-flow__shape--' + node.type,
          cx: w / 2,
          cy: h / 2,
          r: w / 2 - 4
        }));
        if (node.type === 'start') {
          group.appendChild(svg('path', {
            'class': 'yd-flow__glyph',
            d: 'M ' + (w / 2 - 7) + ' ' + (h / 2 - 10) + ' L ' + (w / 2 + 11) + ' ' + (h / 2) + ' L ' + (w / 2 - 7) + ' ' + (h / 2 + 10) + ' Z'
          }));
        } else {
          group.appendChild(svg('circle', {
            'class': 'yd-flow__glyph' + (node.terminate ? ' yd-flow__glyph--solid' : ''),
            cx: w / 2,
            cy: h / 2,
            r: node.terminate ? 12 : 9
          }));
        }
      } else if (node.type === 'exclusive' || node.type === 'parallel' || node.type === 'inclusive' || node.type === 'event') {
        var cx = w / 2;
        var cy = h / 2;
        group.appendChild(svg('polygon', {
          'class': 'yd-flow__shape yd-flow__shape--gateway',
          points: cx + ',4 ' + (w - 4) + ',' + cy + ' ' + cx + ',' + (h - 4) + ' 4,' + cy
        }));
        var glyph;
        if (node.type === 'exclusive' || node.type === 'event') {
          glyph = svg('path', {
            'class': 'yd-flow__glyph',
            d: 'M ' + (cx - 11) + ' ' + (cy - 11) + ' L ' + (cx + 11) + ' ' + (cy + 11) + ' M ' + (cx + 11) + ' ' + (cy - 11) + ' L ' + (cx - 11) + ' ' + (cy + 11)
          });
          group.appendChild(glyph);
        } else if (node.type === 'parallel') {
          group.appendChild(svg('path', {
            'class': 'yd-flow__glyph',
            d: 'M ' + (cx - 11) + ' ' + cy + ' L ' + (cx + 11) + ' ' + cy + ' M ' + cx + ' ' + (cy - 11) + ' L ' + cx + ' ' + (cy + 11)
          }));
        } else {
          group.appendChild(svg('circle', {
            'class': 'yd-flow__glyph',
            cx: cx,
            cy: cy,
            r: 11
          }));
        }
      } else if (node.type === 'catch') {
        group.appendChild(svg('circle', {
          'class': 'yd-flow__shape yd-flow__shape--catch',
          cx: w / 2,
          cy: h / 2,
          r: w / 2 - 4
        }));
        group.appendChild(svg('circle', {
          'class': 'yd-flow__glyph',
          cx: w / 2,
          cy: h / 2,
          r: w / 2 - 11
        }));
        if (node.wait_kind === 'message') {
          group.appendChild(svg('path', {
            'class': 'yd-flow__glyph',
            d: 'M ' + (w / 2 - 9) + ' ' + (h / 2 - 6) + ' L ' + (w / 2 + 9) + ' ' + (h / 2 - 6) + ' L ' + (w / 2 + 9) + ' ' + (h / 2 + 7) + ' L ' + (w / 2 - 9) + ' ' + (h / 2 + 7) + ' Z'
          }));
        } else {
          group.appendChild(svg('path', {
            'class': 'yd-flow__glyph',
            d: 'M ' + (w / 2 - 8) + ' ' + (h / 2) + ' L ' + (w / 2) + ' ' + (h / 2 - 8) + ' L ' + (w / 2) + ' ' + (h / 2 + 8) + ' Z'
          }));
        }
      } else {
        group.appendChild(svg('rect', {
          'class': 'yd-flow__shape yd-flow__shape--' + node.type,
          x: 2,
          y: 2,
          width: w - 4,
          height: h - 4,
          rx: node.type === 'user' ? 12 : 4
        }));
        group.appendChild(svg('text', {
          'class': 'yd-flow__badge',
          x: 14,
          y: 20
        }, [document.createTextNode(node.type === 'user' ? L('label_approval', 'Approval') : L('label_service', 'Service'))]));
        group.appendChild(svg('text', {
          'class': 'yd-flow__label',
          x: w / 2,
          y: h / 2 + 8,
          'text-anchor': 'middle'
        }, [document.createTextNode(truncate(node.name || node.id, 18))]));
        return group;
      }

      group.appendChild(svg('text', {
        'class': 'yd-flow__label yd-flow__label--below',
        x: w / 2,
        y: h + 18,
        'text-anchor': 'middle'
      }, [document.createTextNode(truncate(node.name || node.id, 16))]));
      return group;
    }

    function drawEdge(edge) {
      var source = findNode(edge.source);
      var target = findNode(edge.target);
      if (!source || !target) {
        return null;
      }
      var from = portOf(source, centerOf(target));
      var to = portOf(target, centerOf(source));
      var path = 'M ' + from.x + ' ' + from.y + ' L ' + to.x + ' ' + to.y;
      var group = svg('g', {
        'class': 'yd-flow__edge' + (isSelected('edge', edge.id) ? ' is-selected' : ''),
        'data-edge-id': edge.id
      });
      group.appendChild(svg('path', {
        'class': 'yd-flow__edge-hit',
        d: path
      }));
      group.appendChild(svg('path', {
        'class': 'yd-flow__edge-line',
        d: path,
        'marker-end': 'url(#yd-flow-arrow)'
      }));
      if (edge.name) {
        var midX = (from.x + to.x) / 2;
        var midY = (from.y + to.y) / 2;
        group.appendChild(svg('text', {
          'class': 'yd-flow__edge-label',
          x: midX,
          y: midY - 8,
          'text-anchor': 'middle'
        }, [document.createTextNode(truncate(edge.name, 24))]));
      }
      return group;
    }

    function renderCanvas() {
      layers.textContent = '';
      design.edges.forEach(function (edge) {
        var drawn = drawEdge(edge);
        if (drawn) {
          layers.appendChild(drawn);
        }
      });
      design.nodes.forEach(function (node) {
        layers.appendChild(drawShape(node));
      });
      bindCanvasEvents();
    }

    function applyZoom() {
      stage.style.transform = 'scale(' + state.zoom + ')';
      setStatus(L('label_zoom', 'Zoom') + ' ' + Math.round(state.zoom * 100) + '%');
    }

    // -----------------------------------------------------------------------
    // 画布交互 | Canvas interactions
    // -----------------------------------------------------------------------

    function canvasPoint(event) {
      var rect = stage.getBoundingClientRect();
      return {
        x: (event.clientX - rect.left) / state.zoom,
        y: (event.clientY - rect.top) / state.zoom
      };
    }

    function select(kind, id) {
      state.selected = { kind: kind, id: id };
      state.connectFrom = null;
      renderCanvas();
      renderInspector();
    }

    function clearSelection() {
      state.selected = null;
      state.connectFrom = null;
      renderCanvas();
      renderInspector();
    }

    function bindCanvasEvents() {
      Array.prototype.forEach.call(layers.querySelectorAll('[data-node-id]'), function (node) {
        node.addEventListener('pointerdown', function (event) {
          event.stopPropagation();
          event.preventDefault();
          var id = node.getAttribute('data-node-id');
          if (state.connect) {
            if (!state.connectFrom) {
              state.connectFrom = id;
              select('node', id);
              setStatus(L('label_pick_the_target_node', 'Pick the target node'));
              return;
            }
            if (state.connectFrom === id) {
              setStatus(L('label_a_node_cannot_connect_to_itself', 'A node cannot connect to itself'), 'warn');
              return;
            }
            addEdge(state.connectFrom, id);
            state.connectFrom = null;
            return;
          }
          select('node', id);
          var model = findNode(id);
          if (!model) {
            return;
          }
          state.drag = {
            id: id,
            start: canvasPoint(event),
            origin: { x: Number(model.position && model.position.x) || 0, y: Number(model.position && model.position.y) || 0 },
            moved: false
          };
        });
        node.addEventListener('dblclick', function (event) {
          event.preventDefault();
          select('node', node.getAttribute('data-node-id'));
          var first = inspector.querySelector('input, select, textarea');
          if (first) {
            first.focus();
          }
        });
      });
      Array.prototype.forEach.call(layers.querySelectorAll('[data-edge-id]'), function (edgeNode) {
        edgeNode.addEventListener('pointerdown', function (event) {
          event.stopPropagation();
          event.preventDefault();
          select('edge', edgeNode.getAttribute('data-edge-id'));
        });
      });
    }

    function addEdge(source, target) {
      var taken = edgeIDs();
      if (design.edges.some(function (edge) {
        return edge.source === source && edge.target === target;
      })) {
        setStatus(L('label_this_sequence_flow_already_exists', 'This sequence flow already exists'), 'warn');
        return;
      }
      var edge = {
        id: nextID('edge_', taken),
        source: source,
        target: target
      };
      design.edges.push(edge);
      markDirty();
      select('edge', edge.id);
      setStatus(L('label_sequence_flow_added', 'Sequence flow added'), 'ok');
    }

    function addNode(type, point) {
      var taken = nodeIDs();
      var box = boxOf({ type: type });
      var x = Math.max(0, Math.round(point.x - box.w / 2));
      var y = Math.max(0, Math.round(point.y - box.h / 2));
      var node = {
        id: nextID(type + '_', taken),
        type: type,
        name: nodeLabel(type),
        position: { x: x, y: y }
      };
      if (type === 'user') {
        node.assignee = { type: 'initiator' };
        node.completion = { mode: 'any' };
      }
      if (type === 'catch') {
        node.wait_kind = 'timer';
        node.wait_for = '1h';
      }
      design.nodes.push(node);
      markDirty();
      select('node', node.id);
      setStatus(L('label_node_added', 'Node added'), 'ok');
    }

    function deleteSelection() {
      if (!state.selected) {
        setStatus(L('label_select_a_node_or_a_flow_first', 'Select a node or a flow first'), 'warn');
        return;
      }
      if (state.selected.kind === 'node') {
        var id = state.selected.id;
        design.nodes = design.nodes.filter(function (node) {
          return node.id !== id;
        });
        design.edges = design.edges.filter(function (edge) {
          return edge.source !== id && edge.target !== id;
        });
        if (design.forms[id]) {
          delete design.forms[id];
        }
      } else {
        var edgeID = state.selected.id;
        design.edges = design.edges.filter(function (edge) {
          return edge.id !== edgeID;
        });
      }
      markDirty();
      clearSelection();
      setStatus(L('label_deleted', 'Deleted'), 'ok');
    }

    // Drag with pointer events on the document so the pointer may leave the stage. |
    // 使用文档级指针事件实现拖拽，指针离开画布也不会中断。
    document.addEventListener('pointermove', function (event) {
      if (!state.drag) {
        return;
      }
      var point = canvasPoint(event);
      var dx = point.x - state.drag.start.x;
      var dy = point.y - state.drag.start.y;
      if (Math.abs(dx) > 1 || Math.abs(dy) > 1) {
        state.drag.moved = true;
      }
      var node = findNode(state.drag.id);
      if (!node) {
        return;
      }
      node.position = {
        x: Math.max(0, Math.round(state.drag.origin.x + dx)),
        y: Math.max(0, Math.round(state.drag.origin.y + dy))
      };
      renderCanvas();
    });
    document.addEventListener('pointerup', function () {
      if (state.drag) {
        if (state.drag.moved) {
          markDirty();
        }
        state.drag = null;
      }
    });

    canvas.addEventListener('pointerdown', function (event) {
      if (event.target === stage || event.target === layers || event.target === canvas) {
        clearSelection();
      }
    });

    canvas.addEventListener('dragover', function (event) {
      event.preventDefault();
      if (event.dataTransfer) {
        event.dataTransfer.dropEffect = 'copy';
      }
    });

    canvas.addEventListener('drop', function (event) {
      event.preventDefault();
      var type = '';
      try {
        type = event.dataTransfer.getData('text/yd-workflow-node');
      } catch (err) {
        type = '';
      }
      if (!NODE_KEYS[type]) {
        return;
      }
      addNode(type, canvasPoint(event));
    });

    Array.prototype.forEach.call(root.querySelectorAll('[data-node-type]'), function (button) {
      button.addEventListener('dragstart', function (event) {
        if (event.dataTransfer) {
          event.dataTransfer.setData('text/yd-workflow-node', button.getAttribute('data-node-type'));
          event.dataTransfer.effectAllowed = 'copy';
        }
      });
      button.addEventListener('click', function () {
        addNode(button.getAttribute('data-node-type'), { x: 260, y: 160 + design.nodes.length * 24 });
      });
    });

    // -----------------------------------------------------------------------
    // 属性面板控件 | Inspector widgets
    // -----------------------------------------------------------------------

    function labelled(text, control, hint) {
      var wrap = el('div', { className: 'yd-field' });
      var labelables = [];
      if (control && control.matches && control.matches('input, select, textarea')) {
        labelables = [control];
      } else if (control && control.querySelectorAll) {
        labelables = Array.prototype.slice.call(control.querySelectorAll('input, select, textarea'));
      }
      var single = labelables.length === 1 ? labelables[0] : null;
      var id = single && single.id ? single.id : '';
      var label;
      if (single) {
        label = el('label', { className: 'yd-field__label', text: text });
        if (id) {
          label.setAttribute('for', id);
        }
      } else {
        label = el('span', { className: 'yd-field__label', text: text });
        if (labelables.length > 1) {
          var groupID = nextControlID('yd-flow-group');
          label.id = groupID;
          wrap.setAttribute('role', 'group');
          wrap.setAttribute('aria-labelledby', groupID);
        }
      }
      wrap.appendChild(label);
      if (control) {
        wrap.appendChild(control);
      }
      if (hint) {
        var hintID = id ? id + '-hint' : '';
        var hintNode = el('span', { className: 'yd-field__hint', text: hint });
        if (hintID) {
          hintNode.id = hintID;
          if (single) {
            single.setAttribute('aria-describedby', hintID);
          } else if (labelables.length > 1) {
            wrap.setAttribute('aria-describedby', hintID);
          }
        }
        wrap.appendChild(hintNode);
      }
      if (control && control.querySelector) {
        var trigger = control.querySelector('.yd-selectbox__button');
        if (trigger) {
          trigger.setAttribute('aria-label', text);
        }
      }
      return wrap;
    }

    function textInput(value, onInput, attrs) {
      var input = el('input', {
        className: 'yd-input',
        type: (attrs && attrs.type) || 'text',
        value: value === null || value === undefined ? '' : String(value)
      });
      input.id = attrs && attrs.id ? attrs.id : nextControlID('yd-flow-input');
      if (attrs && attrs.placeholder) {
        input.placeholder = attrs.placeholder;
      }
      if (attrs && attrs.maxLength) {
        input.maxLength = attrs.maxLength;
      }
      if (attrs && attrs.readOnly) {
        input.readOnly = true;
      }
      if (attrs && attrs.ariaLabel) {
        input.setAttribute('aria-label', attrs.ariaLabel);
      }
      input.addEventListener('input', function () {
        onInput(input.value);
      });
      return input;
    }

    function numberInput(value, onInput, attrs) {
      var input = textInput(value, function (raw) {
        var parsed = Number(raw);
        onInput(isFinite(parsed) ? parsed : 0);
      }, attrs || {});
      input.type = 'number';
      if (attrs && attrs.min !== undefined) {
        input.min = String(attrs.min);
      }
      if (attrs && attrs.max !== undefined) {
        input.max = String(attrs.max);
      }
      if (attrs && attrs.step !== undefined) {
        input.step = String(attrs.step);
      }
      return input;
    }

    function textArea(value, onInput, rows) {
      var area = el('textarea', {
        className: 'yd-textarea',
        rows: rows || 3
      });
      area.id = nextControlID('yd-flow-textarea');
      area.value = value === null || value === undefined ? '' : String(value);
      area.addEventListener('input', function () {
        onInput(area.value);
      });
      return area;
    }

    function selectInput(value, options, onChange, ariaLabel) {
      var id = nextControlID('yd-flow-select');
      var selectedLabel = '';
      var select = el('select', { className: 'yd-select', id: id });
      options.forEach(function (option) {
        var item = el('option', { value: option.value, text: option.label });
        if (String(option.value) === String(value === undefined || value === null ? '' : value)) {
          item.selected = true;
          selectedLabel = option.label;
        }
        select.appendChild(item);
      });
      if (!selectedLabel && options.length) {
        selectedLabel = options[0].label;
      }
      if (ariaLabel) {
        select.setAttribute('aria-label', ariaLabel);
      }
      select.setAttribute('x-ref', 'native');
      select.setAttribute('x-show', '!enhanced');
      select.setAttribute('x-on:change', 'sync');
      select.addEventListener('change', function () {
        var next = select.value;
        // Let ydSelect finish its choose() path before a mode/type change rebuilds the inspector. |
        // 让 ydSelect 完成 choose() 后再由模式/类型变更重建属性面板。
        if (window.Alpine && typeof window.Alpine.initTree === 'function') {
          setTimeout(function () {
            onChange(next);
          }, 0);
        } else {
          onChange(next);
        }
      });

      var box = el('div', { className: 'yd-selectbox' });
      box.setAttribute('data-selected-label', selectedLabel);
      box.setAttribute('x-data', 'ydSelect');
      box.setAttribute('x-on:click.outside', 'close(false)');
      box.appendChild(select);

      var trigger = el('button', {
        className: 'yd-input yd-selectbox__button',
        type: 'button'
      });
      trigger.setAttribute('x-show', 'enhanced');
      trigger.setAttribute('x-cloak', '');
      trigger.setAttribute('x-on:click', 'toggle');
      trigger.setAttribute('x-on:keydown', 'onKey($event)');
      trigger.setAttribute('x-ref', 'trigger');
      trigger.setAttribute('x-bind:aria-expanded', 'open');
      trigger.setAttribute('aria-haspopup', 'listbox');
      trigger.setAttribute('aria-controls', id + '-menu');
      if (ariaLabel) {
        trigger.setAttribute('aria-label', ariaLabel);
      }
      var valueNode = el('span', {
        className: 'yd-selectbox__value',
        text: selectedLabel
      });
      valueNode.setAttribute('x-text', 'selectedLabel');
      trigger.appendChild(valueNode);
      var arrow = el('span', { className: 'yd-selectbox__arrow' });
      arrow.setAttribute('aria-hidden', 'true');
      var arrowIcon = cloneIcon('yd-flow-icon-chevron-down');
      if (arrowIcon) {
        arrow.appendChild(arrowIcon);
      }
      trigger.appendChild(arrow);
      box.appendChild(trigger);

      var menu = el('div', { className: 'yd-selectbox__menu', id: id + '-menu' });
      menu.setAttribute('x-show', 'open');
      menu.setAttribute('x-cloak', '');
      menu.setAttribute('x-ref', 'menu');
      menu.setAttribute('x-on:keydown', 'onKey($event)');
      menu.setAttribute('role', 'listbox');
      menu.setAttribute('tabindex', '-1');
      if (ariaLabel) {
        menu.setAttribute('aria-label', ariaLabel);
      }
      var optionTemplate = el('template');
      optionTemplate.setAttribute('x-for', '(option, index) in options');
      optionTemplate.setAttribute(':key', 'index');
      var optionButton = el('button', {
        className: 'yd-selectbox__option',
        type: 'button'
      });
      optionButton.setAttribute('role', 'option');
      optionButton.setAttribute('x-bind:class', "option.active ? 'yd-selectbox__option--active' : ''");
      optionButton.setAttribute('x-bind:aria-selected', 'option.selected');
      optionButton.setAttribute('x-bind:disabled', 'option.disabled');
      optionButton.setAttribute('x-on:click', 'choose(option.value)');
      optionButton.setAttribute('x-text', 'option.label');
      // Children of a template belong in its content fragment: appendChild(template, child) would
      // leave the node outside the cloned row and make x-for evaluate `option` without a scope. |
      // template 的子节点必须放进 content：直接 appendChild 会把节点留在克隆行之外，
      // 让 x-for 在没有作用域的情况下求值 `option`。
      optionTemplate.content.appendChild(optionButton);
      menu.appendChild(optionTemplate);
      box.appendChild(menu);
      return box;
    }

    function checkboxLine(value, label, checked, onChange) {
      var id = nextControlID('yd-flow-check');
      var input = el('input', { className: 'yd-check__input', type: 'checkbox', id: id });
      if (value !== undefined && value !== null && value !== '') {
        input.value = value;
      }
      input.checked = Boolean(checked);
      input.addEventListener('change', function () {
        onChange(input.checked);
      });
      var line = el('label', { className: 'yd-check' });
      line.setAttribute('for', id);
      var body = el('span', { className: 'yd-check__body' });
      body.appendChild(el('span', { className: 'yd-check__label', text: label }));
      line.appendChild(input);
      line.appendChild(body);
      return line;
    }

    function checkboxInput(checked, label, onChange) {
      return checkboxLine('', label, checked, onChange);
    }

    function valueList(values, choices, onChange) {
      var wrap = el('div', { className: 'yd-check-list' });
      var selected = {};
      (values || []).forEach(function (value) {
        selected[value] = true;
      });
      if (!choices.length) {
        wrap.appendChild(el('p', { className: 'yd-field__hint', text: L('label_no_candidate_is_available', 'No candidate is available.') }));
        return wrap;
      }
      choices.forEach(function (choice) {
        wrap.appendChild(checkboxLine(choice.value, choice.label, selected[choice.value], function () {
          var out = [];
          Array.prototype.forEach.call(wrap.querySelectorAll('input[type=checkbox]'), function (item) {
            if (item.checked) {
              out.push(item.value);
            }
          });
          onChange(out);
        }));
      });
      return wrap;
    }

    function section(title, children) {
      var block = el('section', { className: 'yd-flow__section' });
      block.appendChild(el('h3', { className: 'yd-flow__section-title', text: title }));
      (children || []).forEach(function (child) {
        if (child) {
          block.appendChild(child);
        }
      });
      return block;
    }

    function actionButton(label, handler, variant, confirmText, ariaLabel) {
      var button = el('button', {
        className: 'yd-btn yd-btn--' + (variant || 'ghost') + ' yd-btn--sm',
        type: 'button',
        text: label
      });
      if (ariaLabel) {
        button.setAttribute('aria-label', ariaLabel);
      }
      button.addEventListener('click', function () {
        askConfirm(confirmText, handler);
      });
      return button;
    }

    // -----------------------------------------------------------------------
    // 属性面板：流程 | Inspector: process
    // -----------------------------------------------------------------------

    function renderProcessInspector() {
      inspector.textContent = '';
      inspector.appendChild(el('h2', { className: 'yd-flow__section-title', text: L('label_process_settings', 'Process settings') }));

      var settings = design.settings;
      inspector.appendChild(labelled(L('label_version_note', 'Version note'), textInput(state.note, function (value) {
        state.note = value;
        markDirty();
      }, { maxLength: 500 })));
      inspector.appendChild(labelled(L('label_description', 'Description'), textArea(settings.description || '', function (value) {
        settings.description = value;
        markDirty();
      }, 3)));

      var due = labelled(L('label_default_deadline', 'Default deadline'), textInput(settings.due || '', function (value) {
        settings.due = value;
        markDirty();
      }, { placeholder: '24h' }));
      inspector.appendChild(due);

      inspector.appendChild(labelled(L('label_withdrawal_window', 'Withdrawal window'), textInput(settings.withdraw_window || '', function (value) {
        settings.withdraw_window = value;
        markDirty();
      }, { placeholder: '10m' })));

      ['unique_business_key', 'forbid_return', 'forbid_delegate', 'forbid_add_sign'].forEach(function (key) {
        var labels = {
          unique_business_key: [L('label_unique_business_key', 'Unique business key'), L('label_reject_a_second_running_instance_with_the_same_key', 'Reject a second running instance with the same key')],
          forbid_return: [L('label_forbid_return', 'Forbid return'), ''],
          forbid_delegate: [L('label_forbid_delegation', 'Forbid delegation'), ''],
          forbid_add_sign: [L('label_forbid_add_remove_sign', 'Forbid add/remove sign'), '']
        };
        inspector.appendChild(checkboxInput(settings[key], labels[key][0], function (checked) {
          settings[key] = checked;
          markDirty();
        }));
      });

      inspector.appendChild(renderStartFormEditor());
      inspector.appendChild(renderRulesEditor());
    }

    // -----------------------------------------------------------------------
    // 属性面板：节点与连线 | Inspector: nodes and edges
    // -----------------------------------------------------------------------

    function renderNodeInspector(node) {
      inspector.textContent = '';
      inspector.appendChild(el('h2', { className: 'yd-flow__section-title', text: node.name || node.id }));
      inspector.appendChild(el('p', { className: 'yd-field__hint', text: node.id + ' · ' + nodeLabel(node.type) }));

      inspector.appendChild(labelled(L('label_name', 'Name'), textInput(node.name || '', function (value) {
        node.name = value;
        markDirty();
        renderCanvas();
      }, { maxLength: 120 }), node.name === '' ? L('label_a_node_needs_a_name', 'A node needs a name') : ''));

      inspector.appendChild(labelled(L('label_english_name', 'English name'), textInput(node.name_en || '', function (value) {
        node.name_en = value;
        markDirty();
      }, { maxLength: 120 })));

      inspector.appendChild(labelled(L('label_description', 'Description'), textArea(node.description || '', function (value) {
        node.description = value;
        markDirty();
      }, 2)));

      if (node.type === 'user') {
        renderUserTaskInspector(node);
      } else if (node.type === 'catch') {
        renderCatchInspector(node);
      } else if (node.type === 'end') {
        inspector.appendChild(checkboxInput(node.terminate, L('label_terminate_the_whole_process', 'Terminate the whole process'), function (checked) {
          node.terminate = checked;
          markDirty();
        }));
      } else if (node.type === 'start') {
        inspector.appendChild(renderNodeFormEditor(node, 'start'));
      }

      inspector.appendChild(el('div', { className: 'yd-flow__actions-row' }, [
        actionButton(L('label_delete_node', 'Delete node'), deleteSelection, 'danger')
      ]));
    }

    function renderUserTaskInspector(node) {
      node.assignee = node.assignee || { type: 'initiator' };
      node.completion = node.completion || { mode: 'any' };
      var assignee = node.assignee;
      var completion = node.completion;

      inspector.appendChild(section(L('label_assignees', 'Assignees'), [
        labelled(L('label_assignment', 'Assignment'), selectInput(assignee.type, [
          { value: 'initiator', label: L('label_initiator', 'Initiator') },
          { value: 'user', label: L('label_users', 'Users') },
          { value: 'role', label: L('label_roles', 'Roles') },
          { value: 'group', label: L('label_workflow_groups', 'Workflow groups') }
        ], function (value) {
          assignee.type = value;
          assignee.values = [];
          markDirty();
          renderInspector();
        })),
        assignee.type === 'user' ? labelled(L('label_users_2', 'Users'), valueList(assignee.values, (directory.users || []).map(function (user) {
          return { value: user.id, label: user.name + ' (' + user.username + ')' };
        }), function (values) {
          assignee.values = values;
          markDirty();
        })) : null,
        assignee.type === 'role' ? labelled(L('label_roles', 'Roles'), valueList(assignee.values, (directory.roles || []).map(function (role) {
          return { value: role, label: role };
        }), function (values) {
          assignee.values = values;
          markDirty();
        })) : null,
        assignee.type === 'group' ? labelled(L('label_groups', 'Groups'), valueList(assignee.values, (directory.groups || []).map(function (group) {
          return { value: group.key, label: group.name + ' (' + group.key + ')' };
        }), function (values) {
          assignee.values = values;
          markDirty();
        })) : null,
        labelled(L('label_claim_mode', 'Claim mode'), selectInput(assignee.claim || 'assigned', [
          { value: 'assigned', label: L('label_assigned_directly', 'Assigned directly') },
          { value: 'claim', label: L('label_claim_before_acting', 'Claim before acting') },
          { value: 'grab', label: L('label_grab_first_claimer_wins', 'Grab (first claimer wins)') }
        ], function (value) {
          assignee.claim = value;
          markDirty();
        }))
      ]));

      inspector.appendChild(section(L('label_completion', 'Completion'), [
        labelled(L('label_mode', 'Mode'), selectInput(completion.mode || 'any', [
          { value: 'any', label: L('label_or_sign_any_approver', 'Or-sign (any approver)') },
          { value: 'all', label: L('label_countersign_everyone', 'Countersign (everyone)') },
          { value: 'vote', label: L('label_vote_sign_threshold', 'Vote-sign (threshold)') }
        ], function (value) {
          completion.mode = value;
          markDirty();
          renderInspector();
        })),
        completion.mode === 'vote' ? labelled(L('label_threshold_type', 'Threshold type'), selectInput(completion.threshold_type || 'count', [
          { value: 'count', label: L('label_approval_count', 'Approval count') },
          { value: 'percent', label: L('label_percentage', 'Percentage') }
        ], function (value) {
          completion.threshold_type = value;
          markDirty();
        })) : null,
        completion.mode === 'vote' ? labelled(L('label_threshold', 'Threshold'), numberInput(completion.threshold || 1, function (value) {
          completion.threshold = value;
          markDirty();
        }, { min: 0, step: 1 })) : null,
        completion.mode === 'any' ? null : checkboxInput(completion.sequential, L('label_sequential_one_at_a_time', 'Sequential (one at a time)'), function (checked) {
          completion.sequential = checked;
          markDirty();
        }),
        labelled(L('label_on_rejection', 'On rejection'), selectInput(completion.reject_mode || 'any', [
          { value: 'any', label: L('label_reject_on_the_first_rejection', 'Reject on the first rejection') },
          { value: 'all', label: L('label_reject_only_when_all_reject', 'Reject only when all reject') }
        ], function (value) {
          completion.reject_mode = value;
          markDirty();
        }))
      ]));

      inspector.appendChild(section(L('label_routing_and_timing', 'Routing and timing'), [
        labelled(L('label_on_reject', 'On reject'), selectInput(node.on_reject || '', [
          { value: '', label: L('label_default_terminate', 'Default (terminate)') },
          { value: 'terminate', label: L('label_terminate', 'Terminate') },
          { value: 'end', label: L('label_end_the_process', 'End the process') },
          { value: 'return_initiator', label: L('label_return_to_the_initiator', 'Return to the initiator') }
        ], function (value) {
          node.on_reject = value;
          markDirty();
        })),
        labelled(L('label_deadline', 'Deadline'), textInput(node.due || '', function (value) {
          node.due = value;
          markDirty();
        }, { placeholder: '24h' })),
        labelled(L('label_remind_every', 'Remind every'), textInput(node.remind_every || '', function (value) {
          node.remind_every = value;
          markDirty();
        }, { placeholder: '4h' })),
        labelled(L('label_reminder_ceiling', 'Reminder ceiling'), numberInput(node.max_remind || 0, function (value) {
          node.max_remind = value;
          markDirty();
        }, { min: 0 })),
        checkboxInput(node.allow_auto, L('label_allow_automatic_approval_rules', 'Allow automatic approval rules'), function (checked) {
          node.allow_auto = checked;
          markDirty();
        }),
        labelled(L('label_ai_prompt', 'AI prompt'), textArea(node.ai_prompt || '', function (value) {
          node.ai_prompt = value;
          markDirty();
        }, 2))
      ]));

      inspector.appendChild(renderNodeFormEditor(node, node.id));
    }

    function renderCatchInspector(node) {
      node.wait_kind = node.wait_kind || 'timer';
      inspector.appendChild(section(L('label_wait_event', 'Wait event'), [
        labelled(L('label_kind', 'Kind'), selectInput(node.wait_kind, [
          { value: 'timer', label: L('label_timer', 'Timer') },
          { value: 'message', label: L('label_message_signal', 'Message signal') }
        ], function (value) {
          node.wait_kind = value;
          markDirty();
          renderInspector();
        })),
        node.wait_kind === 'message'
          ? labelled(L('label_signal_name', 'Signal name'), textInput(node.message_name || '', function (value) {
            node.message_name = value;
            markDirty();
          }, { maxLength: 128 }))
          : labelled(L('label_duration', 'Duration'), textInput(node.wait_for || '', function (value) {
            node.wait_for = value;
            markDirty();
          }, { placeholder: '1h / PT1H' }))
      ]));
    }

    function renderEdgeInspector(edge) {
      inspector.textContent = '';
      inspector.appendChild(el('h2', { className: 'yd-flow__section-title', text: L('label_sequence_flow', 'Sequence flow') }));
      inspector.appendChild(el('p', { className: 'yd-field__hint', text: edge.source + ' → ' + edge.target }));
      inspector.appendChild(labelled(L('label_name', 'Name'), textInput(edge.name || '', function (value) {
        edge.name = value;
        markDirty();
        renderCanvas();
      }, { maxLength: 120 })));
      inspector.appendChild(labelled(L('label_condition', 'Condition'), textInput(edge.condition || '', function (value) {
        edge.condition = value;
        markDirty();
      }, { placeholder: "amount > 1000 && status == 'ok'" })));
      inspector.appendChild(labelled(L('label_sort_order', 'Sort order'), numberInput(edge.sort || 0, function (value) {
        edge.sort = value;
        markDirty();
      }, { min: 0 })));
      inspector.appendChild(checkboxInput(edge.default, L('label_default_flow_of_the_gateway', 'Default flow of the gateway'), function (checked) {
        edge.default = checked;
        markDirty();
      }));
      inspector.appendChild(el('div', { className: 'yd-flow__actions-row' }, [
        actionButton(L('label_delete_flow', 'Delete flow'), deleteSelection, 'danger')
      ]));
    }

    // -----------------------------------------------------------------------
    // 表单字段编辑器 | Form field editor
    // -----------------------------------------------------------------------

    function fieldsFor(scope) {
      if (!design.forms[scope]) {
        design.forms[scope] = [];
      }
      return design.forms[scope];
    }

    function renderNodeFormEditor(node, scope) {
      var fields = fieldsFor(scope);
      var block = section(L('label_form_fields', 'Form fields'), []);
      fields.forEach(function (field, index) {
        var row = el('div', { className: 'yd-flow__field-row' });
        row.appendChild(textInput(field.key || '', function (value) {
          field.key = value;
          markDirty();
        }, { placeholder: L('label_key', 'key'), maxLength: 64 }));
        row.appendChild(textInput(field.label || '', function (value) {
          field.label = value;
          markDirty();
        }, { placeholder: L('label_label', 'Label'), maxLength: 120 }));
        row.appendChild(selectInput(field.type || 'text', [
          { value: 'text', label: L('label_text', 'Text') },
          { value: 'textarea', label: L('label_long_text', 'Long text') },
          { value: 'number', label: L('label_number', 'Number') },
          { value: 'date', label: L('label_date', 'Date') },
          { value: 'datetime', label: L('label_date_and_time', 'Date and time') },
          { value: 'select', label: L('label_select', 'Select') },
          { value: 'checkbox', label: L('label_checkbox', 'Checkbox') }
        ], function (value) {
          field.type = value;
          markDirty();
          renderInspector();
        }, L('label_type', 'Type')));
        row.appendChild(checkboxInput(field.required, L('label_required', 'Required'), function (checked) {
          field.required = checked;
          markDirty();
        }));
        row.appendChild(actionButton('×', function () {
          fields.splice(index, 1);
          if (!fields.length) {
            delete design.forms[scope];
          }
          markDirty();
          renderInspector();
        }, 'ghost'));
        block.appendChild(row);
        if (field.type === 'select') {
          block.appendChild(labelled(L('label_options_value_label_per_line', 'Options (value|label per line)'), textArea(optionsText(field.options), function (value) {
            field.options = parseOptions(value);
            markDirty();
          }, 3)));
        }
      });
      block.appendChild(actionButton(L('label_add_field', 'Add field'), function () {
        fields.push({ key: 'field_' + (fields.length + 1), label: '', type: 'text' });
        markDirty();
        renderInspector();
      }, 'ghost'));
      return block;
    }

    function renderStartFormEditor() {
      return renderNodeFormEditor({ id: 'start' }, 'start');
    }

    // -----------------------------------------------------------------------
    // 自动审批规则 | Automatic approval rules
    // -----------------------------------------------------------------------

    function renderRulesEditor() {
      var block = section(L('label_automatic_approval_rules', 'Automatic approval rules'), []);
      design.rules.forEach(function (rule, index) {
        rule.match = rule.match || [];
        var row = el('div', { className: 'yd-flow__rule' });
        row.appendChild(el('div', { className: 'yd-flow__rule-head' }, [
          textInput(rule.name || '', function (value) {
            rule.name = value;
            markDirty();
          }, { placeholder: L('label_rule_name', 'Rule name'), ariaLabel: L('label_rule_name', 'Rule name'), maxLength: 120 }),
          checkboxInput(rule.enabled, L('label_enabled', 'Enabled'), function (checked) {
            rule.enabled = checked;
            markDirty();
          }),
          actionButton('×', function () {
            design.rules.splice(index, 1);
            markDirty();
            renderInspector();
          }, 'ghost', '', L('label_delete_rule', 'Delete rule'))
        ]));
        row.appendChild(labelled(L('label_event', 'Event'), selectInput(rule.event || 'task', [
          { value: 'start', label: L('label_instance_start', 'Instance start') },
          { value: 'task', label: L('label_task_creation', 'Task creation') }
        ], function (value) {
          rule.event = value;
          markDirty();
        })));
        row.appendChild(labelled(L('label_scope', 'Scope'), selectInput(rule.scope || 'variable', [
          { value: 'category', label: L('label_category', 'Category') },
          { value: 'role', label: L('label_role', 'Role') },
          { value: 'user', label: L('label_user', 'User') },
          { value: 'group', label: L('label_workflow_group', 'Workflow group') },
          { value: 'event', label: L('label_event_name', 'Event name') },
          { value: 'variable', label: L('label_variable_expression', 'Variable expression') }
        ], function (value) {
          rule.scope = value;
          markDirty();
          renderInspector();
        })));
        if (rule.scope === 'variable') {
          row.appendChild(labelled(L('label_condition', 'Condition'), textInput(rule.condition || '', function (value) {
            rule.condition = value;
            markDirty();
          }, { placeholder: "amount <= 500" })));
        } else {
          row.appendChild(labelled(L('label_match_comma_separated', 'Match (comma separated)'), textInput((rule.match || []).join(','), function (value) {
            rule.match = value.split(',').map(function (item) {
              return item.trim();
            }).filter(function (item) {
              return item !== '';
            });
            markDirty();
          })));
        }
        row.appendChild(labelled(L('label_decision', 'Decision'), selectInput(rule.decision || 'approve', [
          { value: 'approve', label: L('label_approve', 'Approve') },
          { value: 'reject', label: L('label_reject', 'Reject') }
        ], function (value) {
          rule.decision = value;
          markDirty();
        })));
        row.appendChild(labelled(L('label_priority', 'Priority'), numberInput(rule.priority || 0, function (value) {
          rule.priority = value;
          markDirty();
        })));
        row.appendChild(checkboxInput(rule.use_ai, L('label_ask_the_ai_decider', 'Ask the AI decider'), function (checked) {
          rule.use_ai = checked;
          markDirty();
        }));
        block.appendChild(row);
      });
      block.appendChild(actionButton(L('label_add_rule', 'Add rule'), function () {
        design.rules.push({
          id: nextID('rule_', {}),
          name: '',
          enabled: true,
          event: 'task',
          scope: 'variable',
          decision: 'approve',
          priority: 0
        });
        markDirty();
        renderInspector();
      }, 'ghost'));
      return block;
    }

    // -----------------------------------------------------------------------
    // 面板调度 | Inspector dispatch
    // -----------------------------------------------------------------------

    function renderInspector() {
      if (window.Alpine && typeof window.Alpine.destroyTree === 'function') {
        try {
          window.Alpine.destroyTree(inspector);
        } catch (err) {
          // The old inspector may already be detached; the new tree is still safe to build. |
          // 旧属性面板可能已经脱离文档；新树仍可安全构建。
        }
      }
      renderInspectorView();
      if (window.Alpine && typeof window.Alpine.initTree === 'function') {
        try {
          window.Alpine.initTree(inspector);
        } catch (err) {
          // Keep the native select fallback usable when Alpine cannot initialize the subtree. |
          // Alpine 无法初始化子树时保留原生 select 回退，控件仍然可用。
        }
      }
    }

    function renderInspectorView() {
      if (!state.selected) {
        renderProcessInspector();
        return;
      }
      if (state.selected.kind === 'node') {
        var node = findNode(state.selected.id);
        if (node) {
          renderNodeInspector(node);
          return;
        }
      } else {
        var edge = findEdge(state.selected.id);
        if (edge) {
          renderEdgeInspector(edge);
          return;
        }
      }
      state.selected = null;
      renderProcessInspector();
    }

    // -----------------------------------------------------------------------
    // 校验、保存与发布 | Validate, save and publish
    // -----------------------------------------------------------------------

    function validateClient() {
      var problems = [];
      var hasStart = design.nodes.some(function (node) {
        return node.type === 'start';
      });
      var hasEnd = design.nodes.some(function (node) {
        return node.type === 'end';
      });
      if (!hasStart) {
        problems.push(L('label_the_design_needs_a_start_event', 'The design needs a start event'));
      }
      if (!hasEnd) {
        problems.push(L('label_the_design_needs_an_end_event', 'The design needs an end event'));
      }
      var ids = {};
      design.nodes.forEach(function (node) {
        if (!node.name) {
          problems.push(L('label_validation_node_no_name', 'Node {id} has no name', { id: node.id }));
        }
        if (ids[node.id]) {
          problems.push(L('label_validation_duplicate_node_id', 'Duplicate node id {id}', { id: node.id }));
        }
        ids[node.id] = true;
        if (node.type === 'user') {
          var assignee = node.assignee || {};
          if (assignee.type !== 'initiator' && (!assignee.values || !assignee.values.length)) {
            problems.push(L('label_validation_node_no_assignee', 'Node {id} has no assignee', { id: node.id }));
          }
        }
        if (node.type === 'catch') {
          if (node.wait_kind === 'message' && !node.message_name) {
            problems.push(L('label_validation_wait_no_signal', 'Wait event {id} has no signal name', { id: node.id }));
          }
          if (node.wait_kind === 'timer' && !node.wait_for) {
            problems.push(L('label_validation_wait_no_duration', 'Wait event {id} has no duration', { id: node.id }));
          }
        }
      });
      design.edges.forEach(function (edge) {
        if (!ids[edge.source] || !ids[edge.target]) {
          problems.push(L('label_validation_edge_missing_node', 'Flow {id} refers to a missing node', { id: edge.id }));
        }
      });
      return problems;
    }

    function reportProblems(problems) {
      if (problems.length) {
        setStatus(problems.join(' · '), 'error');
        return false;
      }
      setStatus(L('label_validation_passed', 'Validation passed'), 'ok');
      return true;
    }

    function apiRequest(method, url, body) {
      return fetch(url, {
        method: method,
        credentials: 'same-origin',
        headers: {
          'Content-Type': 'application/json',
          'Accept': 'application/json',
          'X-Requested-With': 'yd-workflow-designer'
        },
        body: body === undefined ? undefined : JSON.stringify(body)
      }).then(function (response) {
        return response.text().then(function (text) {
          var data = null;
          if (text) {
            try {
              data = JSON.parse(text);
            } catch (err) {
              data = null;
            }
          }
          if (!response.ok) {
            var message = data && (data.error || data.message || data.detail);
            throw new Error(message || (L('label_request_failed', 'Request failed') + ' (' + response.status + ')'));
          }
          return data;
        });
      });
    }

    function saveDraft() {
      if (!reportProblems(validateClient())) {
        return false;
      }
      setStatus(L('label_saving', 'Saving…'));
      return apiRequest('PUT', root.getAttribute('data-save-url'), { design: design, note: state.note }).then(function () {
        state.dirty = false;
        setStatus(L('label_draft_saved', 'Draft saved'), 'ok');
        return true;
      }).catch(function (err) {
        setStatus(err.message, 'error');
        return false;
      });
    }

    function publish() {
      if (!reportProblems(validateClient())) {
        return;
      }
      var confirmText = root.getAttribute('data-confirm-publish') || '';
      askConfirm(confirmText, function () {
        var send = function () {
          setStatus(L('label_publishing', 'Publishing…'));
          apiRequest('POST', root.getAttribute('data-publish-url'), { note: state.note }).then(function () {
            setStatus(L('label_version_published', 'Version published'), 'ok');
            state.dirty = false;
          }).catch(function (err) {
            setStatus(err.message, 'error');
          });
        };
        // 未保存的修改必须先落库；否则发布出去的仍是上一版设计。 | Unsaved edits must reach the
        // draft first; otherwise the published design is the previous version.
        if (state.dirty) {
          var saved = saveDraft();
          if (saved) {
            saved.then(function (ok) {
              if (ok) {
                send();
              }
            });
          }
          return;
        }
        send();
      });
    }

    // -----------------------------------------------------------------------
    // 工具栏 | Toolbar
    // -----------------------------------------------------------------------

    root.querySelectorAll('[data-yd-flow-action]').forEach(function (button) {
      var action = button.getAttribute('data-yd-flow-action');
      if (action === 'export-json' || action === 'export-bpmn') {
        // 导出必须包含当前画布；未保存时先落草稿再让浏览器走服务端下载。 |
        // Exports must reflect the current canvas; save the draft first when it is dirty.
        button.addEventListener('click', function (event) {
          if (!state.dirty) {
            return;
          }
          event.preventDefault();
          var saved = saveDraft();
          if (saved) {
            saved.then(function (ok) {
              if (ok) {
                window.location.href = button.getAttribute('href');
              }
            });
          }
        });
        return;
      }
      button.addEventListener('click', function () {
        switch (action) {
          case 'save':
            saveDraft();
            break;
          case 'publish':
            publish();
            break;
          case 'delete':
            deleteSelection();
            break;
          case 'validate':
            reportProblems(validateClient());
            break;
          case 'connect':
            state.connect = !state.connect;
            state.connectFrom = null;
            button.setAttribute('aria-pressed', state.connect ? 'true' : 'false');
            setStatus(state.connect ? L('label_pick_the_source_node', 'Pick the source node') : L('label_connect_mode_off', 'Connect mode off'));
            break;
          case 'zoom-in':
            state.zoom = Math.min(1.8, state.zoom + 0.1);
            applyZoom();
            break;
          case 'zoom-out':
            state.zoom = Math.max(0.4, state.zoom - 0.1);
            applyZoom();
            break;
          default:
            break;
        }
      });
    });

    root.addEventListener('keydown', function (event) {
      var target = event.target;
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT')) {
        return;
      }
      if (event.key === 'Delete' || event.key === 'Backspace') {
        if (state.selected) {
          event.preventDefault();
          deleteSelection();
        }
      } else if (event.key === 'Escape') {
        state.connect = false;
        state.connectFrom = null;
        clearSelection();
      } else if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's') {
        event.preventDefault();
        saveDraft();
      }
    });

    renderCanvas();
    renderInspector();
    applyZoom();
    setStatus(L('label_ready', 'Ready'));
  }

  function boot() {
    // A language switch re-renders the shell and its label island; refresh before booting the new
    // roots. | 语言切换会重绘外壳与目录岛；先刷新目录再启动新的设计器根节点。
    LABELS = readLabels();
    pruneDesigners();
    Array.prototype.forEach.call(document.querySelectorAll('[data-yd-workflow-designer]'), function (root) {
      if (root.dataset.ydWorkflowDesignerReady === '1') {
        return;
      }
      root.dataset.ydWorkflowDesignerReady = '1';
      try {
        createDesigner(root);
      } catch (err) {
        var status = root.querySelector('[data-yd-flow-status]');
        if (status) {
          status.textContent = err && err.message ? err.message : String(err);
          status.dataset.tone = 'error';
        }
      }
    });
  }

  // htmx 局部导航或页面资产结算后会换入新的设计器 DOM；脚本只加载一次，因此必须重新扫描。
  // An htmx navigation or page-asset settle can swap in a fresh designer DOM; the script is loaded
  // once, so it must rescan and boot the new roots.
  document.addEventListener('htmx:after:swap', boot);
  document.addEventListener('yd:page-assets:loaded', boot);
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
