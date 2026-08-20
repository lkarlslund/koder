(function (root) {
  'use strict';

  const protocol = 'voice.v1';
  const ticketPrefix = 'koder-browser.';
  const frameHeaderSize = 12;

  function randomID() {
    if (root.crypto && typeof root.crypto.randomUUID === 'function') return root.crypto.randomUUID();
    return 'voice-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2);
  }

  function encodePCMFrame(sequence, pcm) {
    const bytes = pcm instanceof Uint8Array ? pcm : new Uint8Array(pcm);
    const frame = new ArrayBuffer(frameHeaderSize + bytes.byteLength);
    const view = new DataView(frame);
    view.setUint8(0, 75); view.setUint8(1, 86); view.setUint8(2, 65); view.setUint8(3, 49);
    view.setUint8(4, 1);
    view.setUint32(8, sequence, false);
    new Uint8Array(frame, frameHeaderSize).set(bytes);
    return frame;
  }

  function decodePCMFrame(payload) {
    const buffer = payload instanceof ArrayBuffer ? payload : payload.buffer;
    const offset = payload instanceof ArrayBuffer ? 0 : payload.byteOffset;
    const length = payload instanceof ArrayBuffer ? payload.byteLength : payload.byteLength;
    if (length < frameHeaderSize) throw new Error('Voice audio frame is truncated');
    const view = new DataView(buffer, offset, length);
    if (view.getUint8(0) !== 75 || view.getUint8(1) !== 86 || view.getUint8(2) !== 65 || view.getUint8(3) !== 49) throw new Error('Voice audio frame has invalid magic');
    if (view.getUint8(4) !== 2) throw new Error('Voice audio frame has unexpected direction');
    return {sequence: view.getUint32(8, false), pcm: new Uint8Array(buffer, offset + frameHeaderSize, length - frameHeaderSize)};
  }

  class PCMResampler {
    constructor(sourceRate, targetRate) {
      this.sourceRate = sourceRate;
      this.targetRate = targetRate;
      this.ratio = sourceRate / targetRate;
      this.carry = new Float32Array(0);
      this.position = 0;
    }

    process(samples) {
      const input = new Float32Array(this.carry.length + samples.length);
      input.set(this.carry);
      input.set(samples, this.carry.length);
      const values = [];
      while (this.position + 1 < input.length) {
        const left = Math.floor(this.position);
        const fraction = this.position - left;
        values.push(input[left] + (input[left + 1] - input[left]) * fraction);
        this.position += this.ratio;
      }
      const consumed = Math.floor(this.position);
      this.carry = input.slice(Math.min(consumed, input.length));
      this.position -= consumed;
      const pcm = new Uint8Array(values.length * 2);
      const view = new DataView(pcm.buffer);
      values.forEach((sample, index) => {
        const clamped = Math.max(-1, Math.min(1, sample));
        view.setInt16(index * 2, clamped < 0 ? clamped * 32768 : clamped * 32767, true);
      });
      return pcm;
    }
  }

  class BrowserVoiceClient {
    constructor(options) {
      this.options = options || {};
      this.callID = randomID();
      this.socket = null;
      this.stream = null;
      this.context = null;
      this.processor = null;
      this.source = null;
      this.silentGain = null;
      this.active = false;
      this.muted = false;
      this.audioConfig = null;
      this.resampler = null;
      this.preRoll = [];
      this.preRollSamples = 0;
      this.speechFrames = 0;
      this.silenceFrames = 0;
      this.noiseFloor = 0.004;
      this.utterance = null;
      this.latestInputUtterance = '';
      this.inputAwaitingResponse = false;
      this.sequence = 0;
      this.utteranceSamples = 0;
      this.outputFormat = null;
      this.outputSequence = 0;
      this.outputSuppressed = false;
      this.playbackTime = 0;
      this.playbackSources = new Set();
      this.playbackEndTimer = null;
      this.reconnectTimer = null;
    }

    emit(type, payload) {
      if (typeof this.options.onEvent === 'function') this.options.onEvent(type, payload || {});
    }

    async start() {
      if (this.active) return;
      this.active = true;
      this.emit('state', {state: 'connecting'});
      try {
        await this.setupAudio();
        await this.connect();
      } catch (error) {
        this.emit('error', {error: error.message || String(error)});
        await this.stop();
        throw error;
      }
    }

    async setupAudio() {
      if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) throw new Error('This browser does not support microphone capture');
      this.stream = await navigator.mediaDevices.getUserMedia({audio: {channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: true}});
      const AudioContextClass = root.AudioContext || root.webkitAudioContext;
      if (!AudioContextClass) throw new Error('This browser does not support Web Audio');
      this.context = new AudioContextClass({latencyHint: 'interactive'});
      await this.context.resume();
      this.source = this.context.createMediaStreamSource(this.stream);
      this.processor = this.context.createScriptProcessor(2048, 1, 1);
      this.silentGain = this.context.createGain();
      this.silentGain.gain.value = 0;
      this.processor.onaudioprocess = event => this.handleMicrophone(event.inputBuffer.getChannelData(0));
      this.source.connect(this.processor);
      this.processor.connect(this.silentGain);
      this.silentGain.connect(this.context.destination);
    }

    async connect() {
      const issued = await this.options.ticketProvider();
      if (!this.active) return;
      const scheme = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const query = new URLSearchParams({call_id: this.callID});
      if (this.options.sessionID) query.set('session_id', this.options.sessionID);
      if (this.options.chatID) query.set('chat_id', this.options.chatID);
      const socket = new WebSocket(scheme + '//' + location.host + '/voice/v1?' + query.toString(), [protocol, ticketPrefix + issued.ticket]);
      socket.binaryType = 'arraybuffer';
      this.socket = socket;
      socket.onopen = () => socket.send(JSON.stringify({type: 'hello', protocol, response_pacing: 'normal'}));
      socket.onmessage = event => this.handleSocketMessage(event);
      socket.onerror = () => this.emit('error', {error: 'The browser voice connection failed'});
      socket.onclose = () => {
        if (this.socket === socket) this.socket = null;
        if (!this.active) return;
        this.emit('state', {state: 'reconnecting'});
        clearTimeout(this.reconnectTimer);
        this.reconnectTimer = setTimeout(() => this.connect().catch(error => this.emit('error', {error: error.message || String(error)})), 1200);
      };
    }

    handleSocketMessage(event) {
      if (typeof event.data !== 'string') {
        try {
          this.playPCMFrame(decodePCMFrame(event.data));
        } catch (error) {
          this.emit('error', {error: error.message});
        }
        return;
      }
      let frame;
      try { frame = JSON.parse(event.data); } catch (_) { return; }
      switch (frame.type) {
      case 'ready':
        this.audioConfig = frame.audio_config;
        this.emit('ready', frame);
        if (this.playbackSources.size === 0 && !this.inputAwaitingResponse) this.emit('state', {state: 'listening'});
        break;
      case 'state':
        this.emit('state', frame);
        break;
      case 'transcript':
        this.emit('transcript', frame);
        break;
      case 'message':
        if (frame.utterance_id === this.latestInputUtterance) this.inputAwaitingResponse = false;
        this.emit('message', frame);
        break;
      case 'render':
        this.emit('render', frame);
        break;
      case 'tts_start':
        this.stopPlayback();
        this.outputFormat = frame.audio_format;
        this.outputSequence = 0;
        this.outputSuppressed = this.outputSuppressed && frame.utterance_id !== this.latestInputUtterance;
        if (!this.outputSuppressed) this.emit('state', {state: 'speaking'});
        break;
      case 'tts_end':
        this.emit('tts_end', frame);
        clearTimeout(this.playbackEndTimer);
        this.playbackEndTimer = setTimeout(() => {
          if (this.active && !this.utterance && !this.inputAwaitingResponse && !this.outputSuppressed) this.emit('state', {state: 'listening'});
        }, Math.max(0, ((this.playbackTime || 0) - this.context.currentTime) * 1000));
        break;
      case 'error':
        this.emit('error', frame);
        this.utterance = null;
        break;
      }
    }

    send(frame) {
      if (!this.socket || this.socket.readyState !== WebSocket.OPEN) return false;
      this.socket.send(JSON.stringify(Object.assign({protocol}, frame)));
      return true;
    }

    handleMicrophone(samples) {
      let sum = 0;
      for (let index = 0; index < samples.length; index++) sum += samples[index] * samples[index];
      const level = Math.sqrt(sum / Math.max(1, samples.length));
      if (typeof this.options.onLevel === 'function') this.options.onLevel(Math.min(1, level * 10));
      if (!this.active || this.muted || !this.audioConfig || !this.socket || this.socket.readyState !== WebSocket.OPEN) return;

      const copy = new Float32Array(samples);
      if (!this.utterance) {
        this.preRoll.push(copy);
        this.preRollSamples += copy.length;
        const maxPreRoll = Math.round(this.context.sampleRate * 0.3);
        while (this.preRollSamples > maxPreRoll && this.preRoll.length > 1) this.preRollSamples -= this.preRoll.shift().length;
      }

      const threshold = Math.max(0.012, this.noiseFloor * 3.2);
      if (!this.utterance && level < threshold) this.noiseFloor = this.noiseFloor * 0.97 + level * 0.03;
      if (level >= threshold) {
        this.speechFrames++;
        this.silenceFrames = 0;
      } else {
        this.speechFrames = 0;
        if (this.utterance) this.silenceFrames++;
      }

      if (!this.utterance && this.speechFrames >= 2) {
        this.beginUtterance();
        return;
      }
      if (!this.utterance) return;
      if (this.preRoll.length === 0) this.sendSamples(copy);

      const silentSeconds = this.silenceFrames * samples.length / this.context.sampleRate;
      const spokenSeconds = this.utteranceSamples / this.context.sampleRate;
      if (silentSeconds >= 0.7 || spokenSeconds >= Number(this.audioConfig.max_utterance_seconds || 60)) this.finishUtterance();
    }

    beginUtterance() {
      this.stopPlayback();
      this.outputSuppressed = true;
      this.utterance = randomID();
      this.latestInputUtterance = this.utterance;
      this.inputAwaitingResponse = true;
      this.sequence = 0;
      this.utteranceSamples = 0;
      this.silenceFrames = 0;
      this.resampler = new PCMResampler(this.context.sampleRate, this.audioConfig.input.sample_rate);
      this.send({type: 'audio_start', utterance_id: this.utterance, audio_format: this.audioConfig.input});
      const buffered = this.preRoll;
      this.preRoll = [];
      this.preRollSamples = 0;
      buffered.forEach(chunk => this.sendSamples(chunk));
      this.emit('state', {state: 'recording'});
    }

    sendSamples(samples) {
      if (!this.utterance || !this.resampler) return;
      this.utteranceSamples += samples.length;
      const pcm = this.resampler.process(samples);
      if (pcm.byteLength === 0) return;
      this.socket.send(encodePCMFrame(this.sequence++, pcm));
    }

    finishUtterance() {
      if (!this.utterance) return;
      const utteranceID = this.utterance;
      this.utterance = null;
      this.resampler = null;
      this.speechFrames = 0;
      this.silenceFrames = 0;
      this.send({type: 'audio_commit', utterance_id: utteranceID, session_id: this.options.sessionID || ''});
      this.emit('state', {state: 'transcribing'});
    }

    setMuted(muted) {
      this.muted = !!muted;
      if (this.muted && this.utterance) {
        this.send({type: 'audio_cancel', utterance_id: this.utterance});
        this.utterance = null;
      }
      this.emit('muted', {muted: this.muted});
    }

    playPCMFrame(frame) {
      if (!this.context || !this.outputFormat || frame.sequence !== this.outputSequence) return;
      this.outputSequence++;
      if (this.outputSuppressed) return;
      const channels = Number(this.outputFormat.channels || 1);
      const input = new DataView(frame.pcm.buffer, frame.pcm.byteOffset, frame.pcm.byteLength);
      const frames = Math.floor(frame.pcm.byteLength / 2 / channels);
      if (!frames) return;
      const buffer = this.context.createBuffer(channels, frames, Number(this.outputFormat.sample_rate));
      for (let channel = 0; channel < channels; channel++) {
        const output = buffer.getChannelData(channel);
        for (let index = 0; index < frames; index++) output[index] = input.getInt16((index * channels + channel) * 2, true) / 32768;
      }
      const source = this.context.createBufferSource();
      source.buffer = buffer;
      source.connect(this.context.destination);
      const startsAt = Math.max(this.context.currentTime + 0.025, this.playbackTime || 0);
      source.start(startsAt);
      this.playbackTime = startsAt + buffer.duration;
      this.playbackSources.add(source);
      source.onended = () => this.playbackSources.delete(source);
    }

    stopPlayback() {
      clearTimeout(this.playbackEndTimer);
      this.playbackSources.forEach(source => { try { source.stop(); } catch (_) {} });
      this.playbackSources.clear();
      this.playbackTime = 0;
    }

    async stop() {
      this.active = false;
      clearTimeout(this.reconnectTimer);
      if (this.utterance) this.send({type: 'audio_cancel', utterance_id: this.utterance});
      this.utterance = null;
      this.stopPlayback();
      if (this.socket) this.socket.close(1000, 'browser voice closed');
      this.socket = null;
      if (this.processor) this.processor.disconnect();
      if (this.source) this.source.disconnect();
      if (this.silentGain) this.silentGain.disconnect();
      if (this.stream) this.stream.getTracks().forEach(track => track.stop());
      if (this.context) await this.context.close().catch(() => {});
      this.processor = this.source = this.silentGain = this.stream = this.context = null;
      this.emit('state', {state: 'idle'});
    }
  }

  root.KoderBrowserVoice = {BrowserVoiceClient, PCMResampler, encodePCMFrame, decodePCMFrame};
})(typeof window !== 'undefined' ? window : globalThis);
