/*!
 * yd-realtime.js — the browser side of the framework's realtime ladder:
 * WebTransport over HTTP/3, WebSocket over HTTP/2/1.1, SSE fallback.
 * yd-realtime.js — 框架实时传输阶梯的浏览器端：HTTP/3 上的 WebTransport、
 * HTTP/2/1.1 上的 WebSocket、SSE 兜底。
 *
 * Why a ladder | 为什么是阶梯
 *   WebTransport is the only browser API that rides HTTP/3 with true
 *   bidirectional streams, but it needs a secure context, a QUIC-capable
 *   network and a browser at Baseline (Chrome/Edge, Firefox, Safari 26.4+).
 *   WebSocket is universal but browsers only use HTTP/2 for it when the server
 *   advertises RFC 8441, and Safari still falls back to HTTP/1.1. SSE works on
 *   every HTTP version and needs no upgrade. Trying them in that order gives
 *   the best transport the current browser and network can actually carry.
 *   WebTransport 是浏览器里唯一能走 HTTP/3 且带真正双向流的 API，但要求安全上下文、
 *   可用的 QUIC 网络与 Baseline 版本的浏览器（Chrome/Edge、Firefox、Safari 26.4+）。
 *   WebSocket 通用，但只有服务端通告 RFC 8441 时浏览器才在 HTTP/2 上使用它，Safari
 *   仍回退 HTTP/1.1。SSE 在所有 HTTP 版本上都可用且无需升级。按此顺序尝试，就能拿到
 *   当前浏览器与网络真正能承载的最好传输。
 *
 * Wire format | 线格式
 *   One uint32 big-endian length followed by that many UTF-8 bytes. A
 *   zero-length frame is a heartbeat; it also opens the WebTransport stream,
 *   because a stream only becomes visible to the server after its first write.
 *   一个大端 uint32 长度加相应数量的 UTF-8 字节。零长度帧是心跳；它同时打开
 *   WebTransport 流——流只有写出过至少一次才对服务端可见。
 *
 * Security | 安全
 *   Authentication is the handshake's job: same-origin cookies, or a token the
 *   page already holds. This file adds no credentials and no fallback that
 *   skips the server's checks. A rendered fragment is swapped into the page, so
 *   the server must have sanitised it; never feed user input into this channel
 *   without escaping it first.
 *   认证由握手负责：同源 cookie，或页面已持有的令牌。本文件不添加任何凭据，也不提供
 *   绕过服务端校验的回退。渲染片段会被交换进页面，因此服务端必须已经净化过它；绝不要把
 *   未转义的用户输入送进这条通道。
 *
 * Public surface | 对外接口
 *   yd.realtime.chooseTransport(caps)      pick a transport name | 选择传输名
 *   yd.realtime.FrameDecoder               incremental frame parser | 增量帧解析器
 *   yd.realtime.encodeFrame(text)          one framed Uint8Array | 一帧字节
 *   new yd.realtime.Channel(options)       connect and swap fragments | 连接并交换片段
 *
 * Channel options | Channel 选项
 *   webtransportURL  https://host/realtime/wt | WebTransport endpoint
 *   websocketURL     wss://host/ws            | WebSocket endpoint
 *   sseURL           /realtime/sse            | SSE endpoint
 *   sseSendURL       POST target for SSE uplink | SSE 上行 POST 地址
 *   target           CSS selector of the swap target | 交换目标的选择器
 *   swap             htmx swap style, default innerMorph | htmx 交换方式
 *   forceTransport   pin "webtransport" | "websocket" | "sse" | 强制指定传输
 */
(function (root, factory) {
  'use strict';
  if (typeof module === 'object' && module.exports) {
    module.exports = factory();
  } else {
    root.yd = root.yd || {};
    root.yd.realtime = factory();
  }
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';

  // TRANSPORTS lists the ladder in preference order. | TRANSPORTS 按优先级列出阶梯。
  var TRANSPORTS = ['webtransport', 'websocket', 'sse'];

  // chooseTransport picks the best transport the environment can carry. |
  // chooseTransport 选择当前环境能承载的最好传输。
  function chooseTransport(caps) {
    caps = caps || {};
    if (caps.force && TRANSPORTS.indexOf(caps.force) >= 0) {
      return caps.force;
    }
    if (caps.secure && caps.webtransport) {
      return 'webtransport';
    }
    if (caps.websocket) {
      return 'websocket';
    }
    if (caps.sse) {
      return 'sse';
    }
    return null;
  }

  // encodeFrame wraps one string in the length-prefixed wire format. |
  // encodeFrame 把一条字符串包进带长度前缀的线格式。
  function encodeFrame(text) {
    var body = new TextEncoder().encode(text);
    var out = new Uint8Array(4 + body.length);
    new DataView(out.buffer).setUint32(0, body.length, false);
    out.set(body, 4);
    return out;
  }

  // FrameDecoder parses frames from arbitrarily split chunks. | FrameDecoder 从任意
  // 切分的块中解析帧。
  function FrameDecoder() {
    this.buffer = new Uint8Array(0);
    /* 复用同一个解码器：每帧 new TextDecoder() 会重复分配内部状态。 |
       Reuse one decoder: creating a TextDecoder per frame re-allocates its internal state. */
    this.decoder = new TextDecoder();
  }

  // push appends one chunk and returns every complete frame it now holds. |
  // push 追加一块数据并返回当前已完整的全部帧。
  FrameDecoder.prototype.push = function (chunk) {
    if (chunk && chunk.length) {
      var merged = new Uint8Array(this.buffer.length + chunk.length);
      merged.set(this.buffer, 0);
      merged.set(chunk, this.buffer.length);
      this.buffer = merged;
    }
    var frames = [];
    var view = new DataView(this.buffer.buffer, this.buffer.byteOffset, this.buffer.byteLength);
    var offset = 0;
    while (this.buffer.length - offset >= 4) {
      var n = view.getUint32(offset, false);
      if (this.buffer.length - offset - 4 < n) {
        break;
      }
      offset += 4;
      if (n === 0) {
        continue; // heartbeat | 心跳
      }
      frames.push(this.decoder.decode(this.buffer.subarray(offset, offset + n)));
      offset += n;
    }
    this.buffer = this.buffer.subarray(offset);
    return frames;
  };

  // Channel owns one live connection and swaps every received fragment into
  // the configured target. | Channel 拥有一条活动连接，并把每个收到的片段交换进配置的目标。
  function Channel(options) {
    options = options || {};
    this.options = options;
    this.target = options.target || '';
    this.swapStyle = options.swap || 'innerMorph';
    this.transport = null;
    this.wt = null;
    this.ws = null;
    this.sse = null;
    this.writer = null;
    this.decoder = new FrameDecoder();
    this.closed = false;
  }

  // caps reports what the environment offers. | caps 报告环境提供的能力。
  Channel.prototype.caps = function () {
    var secure = typeof window !== 'undefined' && (window.isSecureContext === true ||
      (typeof location !== 'undefined' && location.protocol === 'https:'));
    return {
      webtransport: typeof WebTransport === 'function',
      websocket: typeof WebSocket === 'function',
      sse: typeof EventSource === 'function',
      secure: secure === true,
      force: this.options.forceTransport,
    };
  };

  // connect tries the ladder in order and resolves with the chosen transport. |
  // connect 按顺序尝试阶梯，并以选中的传输兑现。
  Channel.prototype.connect = async function () {
    var order = this.options.forceTransport
      ? [this.options.forceTransport]
      : TRANSPORTS.slice();
    var caps = this.caps();
    var firstError = null;
    for (var i = 0; i < order.length; i++) {
      var name = order[i];
      if (!this.available(name, caps)) {
        continue;
      }
      try {
        await this.open(name);
        this.transport = name;
        this.emit('yd:realtime:transport', { transport: name });
        return name;
      } catch (err) {
        firstError = firstError || err;
        this.emit('yd:realtime:error', { transport: name, error: err });
      }
    }
    throw firstError || new Error('yd.realtime: no transport is available | 没有可用的传输');
  };

  // available reports whether one transport can be tried. | available 报告某个传输是否可试。
  Channel.prototype.available = function (name, caps) {
    if (name === 'webtransport') {
      return !!this.options.webtransportURL && caps.webtransport && caps.secure;
    }
    if (name === 'websocket') {
      return !!this.options.websocketURL && caps.websocket;
    }
    if (name === 'sse') {
      return !!this.options.sseURL && caps.sse;
    }
    return false;
  };

  // open establishes one transport. | open 建立一种传输。
  Channel.prototype.open = function (name) {
    if (name === 'webtransport') {
      return this.openWebTransport(this.options.webtransportURL);
    }
    if (name === 'websocket') {
      return this.openWebSocket(this.options.websocketURL);
    }
    if (name === 'sse') {
      return this.openSSE(this.options.sseURL);
    }
    return Promise.reject(new Error('yd.realtime: unknown transport ' + name));
  };

  // openWebTransport opens the QUIC session and the framed control stream. |
  // openWebTransport 打开 QUIC 会话与带帧控制流。
  Channel.prototype.openWebTransport = async function (url) {
    var wt = new WebTransport(url);
    this.wt = wt;
    await wt.ready;
    var stream = await wt.createBidirectionalStream();
    this.writer = stream.writable.getWriter();
    // The heartbeat opens the stream for the server's AcceptStream. | 心跳让服务端
    // 的 AcceptStream 看到这条流。
    await this.writer.write(new Uint8Array([0, 0, 0, 0]));
    this.readStream(stream.readable.getReader());
    var self = this;
    wt.closed.then(function () { self.emit('yd:realtime:close', { transport: 'webtransport' }); })
      .catch(function (err) { self.emit('yd:realtime:close', { transport: 'webtransport', error: err }); });
  };

  // readStream drains one readable stream into the decoder. | readStream 把可读流排空进解码器。
  Channel.prototype.readStream = async function (reader) {
    var self = this;
    try {
      for (;;) {
        var result = await reader.read();
        if (result.done) {
          return;
        }
        self.deliverFrames(self.decoder.push(result.value));
      }
    } catch (err) {
      if (!self.closed) {
        self.emit('yd:realtime:error', { transport: 'webtransport', error: err });
      }
    }
  };

  // openWebSocket opens the RFC 6455 (or RFC 8441) connection. | openWebSocket 打开
  // RFC 6455（或 RFC 8441）连接。
  Channel.prototype.openWebSocket = function (url) {
    var self = this;
    return new Promise(function (resolve, reject) {
      var ws = new WebSocket(url);
      self.ws = ws;
      ws.binaryType = 'arraybuffer';
      ws.onopen = function () { resolve(); };
      ws.onerror = function () { reject(new Error('yd.realtime: websocket failed | WebSocket 失败')); };
      ws.onmessage = function (event) {
        if (typeof event.data === 'string') {
          self.deliverFrames([event.data]);
        } else {
          self.deliverFrames(self.decoder.push(new Uint8Array(event.data)));
        }
      };
      ws.onclose = function () { self.emit('yd:realtime:close', { transport: 'websocket' }); };
    });
  };

  // openSSE opens the fallback stream; uplink goes through sseSendURL. |
  // openSSE 打开兜底流；上行经 sseSendURL。
  Channel.prototype.openSSE = function (url) {
    var self = this;
    return new Promise(function (resolve, reject) {
      var sse = new EventSource(url);
      self.sse = sse;
      sse.onopen = function () { resolve(); };
      sse.onerror = function () { reject(new Error('yd.realtime: sse failed | SSE 失败')); };
      sse.onmessage = function (event) { self.deliverFrames([event.data]); };
    });
  };

  // deliverFrames swaps every frame into the target. | deliverFrames 把每个帧交换进目标。
  Channel.prototype.deliverFrames = function (frames) {
    for (var i = 0; i < frames.length; i++) {
      var html = frames[i];
      if (this.target && typeof window !== 'undefined' && window.htmx && typeof window.htmx.swap === 'function') {
        /* htmx 4 的 swap 只接受一个上下文对象（text/target/swap）；旧的 (target, html, spec) 形式
           会以 TypeError 失败。 | htmx 4's swap takes one context object (text/target/swap); the old
           (target, html, spec) form fails with a TypeError. */
        var target = typeof this.target === 'string' ? document.querySelector(this.target) : this.target;
        if (target) {
          window.htmx.swap({ text: html, sourceElement: target, target: target, swap: this.swapStyle });
        } else {
          this.emit('yd:realtime:message', { html: html });
        }
      } else {
        this.emit('yd:realtime:message', { html: html });
      }
    }
  };

  // send writes one message upstream. | send 向上游写一条消息。
  Channel.prototype.send = async function (text) {
    if (this.transport === 'webtransport' && this.writer) {
      await this.writer.write(encodeFrame(text));
      return;
    }
    if (this.transport === 'websocket' && this.ws && this.ws.readyState === 1) {
      this.ws.send(text);
      return;
    }
    if (this.transport === 'sse' && this.options.sseSendURL) {
      var resp = await fetch(this.options.sseSendURL, {
        method: 'POST',
        headers: { 'Content-Type': 'text/plain; charset=utf-8' },
        body: text,
        credentials: 'same-origin',
      });
      if (!resp.ok) {
        throw new Error('yd.realtime: uplink failed with ' + resp.status);
      }
      return;
    }
    throw new Error('yd.realtime: transport cannot send | 当前传输不能发送');
  };

  // close tears the connection down. | close 拆除连接。
  Channel.prototype.close = function () {
    this.closed = true;
    if (this.wt) {
      try { this.wt.close(); } catch (err) { /* already closed | 已关闭 */ }
    }
    if (this.ws) {
      try { this.ws.close(1000, 'client closed | 客户端关闭'); } catch (err) { /* already closed */ }
    }
    if (this.sse) {
      this.sse.close();
    }
  };

  // emit dispatches a DOM CustomEvent when a document exists. | emit 在存在 document 时
  // 派发 DOM CustomEvent。
  Channel.prototype.emit = function (name, detail) {
    if (typeof window === 'undefined' || typeof window.dispatchEvent !== 'function' ||
        typeof CustomEvent !== 'function') {
      return;
    }
    window.dispatchEvent(new CustomEvent(name, { detail: detail }));
  };

  return {
    TRANSPORTS: TRANSPORTS,
    chooseTransport: chooseTransport,
    encodeFrame: encodeFrame,
    FrameDecoder: FrameDecoder,
    Channel: Channel,
  };
});
