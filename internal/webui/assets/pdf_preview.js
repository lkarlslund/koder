// PDF previews for the file browser, drawn with PDF.js. Pages are laid out
// one below the other at the width of the view; each is drawn when it
// scrolls near and dropped again when it is far away, so long documents
// stay light. A text layer over each page makes its text selectable.
(function () {
  const base = '/assets/vendor/pdfjs/';
  const minZoom = 0.25;
  const maxZoom = 5;
  // Canvas pixels per page; past this the page is drawn at a lower
  // resolution rather than failing or using too much memory.
  const maxCanvasPixels = 2 ** 24;
  const pagePadding = 16;

  let library = null;
  function loadLibrary() {
    library ||= import(base + 'pdf.min.mjs').then(pdfjs => {
      pdfjs.GlobalWorkerOptions.workerSrc = base + 'pdf.worker.min.mjs';
      return pdfjs;
    }).catch(err => {
      library = null;
      throw err;
    });
    return library;
  }

  function clampZoom(value) {
    return Math.min(maxZoom, Math.max(minZoom, Math.round(value * 100) / 100));
  }

  // mount shows the PDF at url in pagesEl, which scroller scrolls.
  // onState receives {loading, error, pages, page, zoom}. The returned
  // viewer is closed with destroy().
  function mount(pagesEl, scroller, {url, onState, scrollTop = 0, zoom = 1}) {
    let destroyed = false;
    let task = null;
    let doc = null;
    let fitScale = 1;
    let epoch = 0; // bumped when the scale changes; older drawings are stale
    let entries = [];
    let observer = null;
    let resizeObserver = null;
    let lastWidth = 0;
    const state = {loading: true, error: '', pages: 0, page: 0, zoom: clampZoom(zoom)};
    const report = changes => {
      Object.assign(state, changes);
      if (!destroyed) onState?.({...state});
    };

    pagesEl.replaceChildren();
    report({});

    const scale = () => fitScale * state.zoom;

    function sizePage(entry, viewport) {
      entry.el.style.width = Math.floor(viewport.width) + 'px';
      entry.el.style.height = Math.floor(viewport.height) + 'px';
      entry.el.style.setProperty('--total-scale-factor', String(viewport.scale));
    }

    function clearPage(entry) {
      entry.renderTask?.cancel();
      entry.textLayer?.cancel();
      entry.renderTask = null;
      entry.textLayer = null;
      entry.drawn = -1;
      entry.el.replaceChildren();
    }

    async function drawPage(entry) {
      if (entry.drawn === epoch || entry.drawing === epoch) return;
      const drawing = epoch;
      entry.drawing = drawing;
      try {
        const pdfjs = await loadLibrary();
        const page = entry.page ||= await doc.getPage(entry.number);
        if (destroyed || drawing !== epoch || !entry.near) return;
        const viewport = page.getViewport({scale: scale()});
        sizePage(entry, viewport);
        const ratio = window.devicePixelRatio || 1;
        const output = Math.min(ratio, Math.sqrt(maxCanvasPixels / (viewport.width * viewport.height)));
        const canvas = document.createElement('canvas');
        canvas.width = Math.floor(viewport.width * output);
        canvas.height = Math.floor(viewport.height * output);
        const text = document.createElement('div');
        text.className = 'textLayer';
        clearPage(entry);
        entry.el.append(canvas, text);
        entry.renderTask = page.render({
          canvas,
          canvasContext: canvas.getContext('2d'),
          viewport,
          transform: output === 1 ? null : [output, 0, 0, output, 0, 0],
        });
        entry.textLayer = new pdfjs.TextLayer({textContentSource: page.streamTextContent(), container: text, viewport});
        await Promise.all([entry.renderTask.promise, entry.textLayer.render()]);
        if (drawing === epoch) entry.drawn = drawing;
      } catch (err) {
        if (err?.name !== 'RenderingCancelledException' && !destroyed) console.warn('PDF page', entry.number, err);
      } finally {
        if (entry.drawing === drawing) entry.drawing = -1;
      }
    }

    function observe() {
      observer?.disconnect();
      observer = new IntersectionObserver(items => {
        for (const item of items) {
          const entry = entries[Number(item.target.dataset.page) - 1];
          if (!entry) continue;
          entry.near = item.isIntersecting;
          if (item.isIntersecting) drawPage(entry);
          else if (entry.drawn !== -1 || entry.renderTask) clearPage(entry);
        }
      }, {root: scroller, rootMargin: '1200px 0px'});
      for (const entry of entries) observer.observe(entry.el);
    }

    function currentPage() {
      const top = scroller.scrollTop + scroller.clientHeight / 3;
      let page = 1;
      for (const entry of entries) {
        if (entry.el.offsetTop - pagesEl.offsetTop <= top) page = entry.number;
        else break;
      }
      return page;
    }
    const onScroll = () => {
      const page = currentPage();
      if (page !== state.page) report({page});
    };

    // relayout sizes every page for the current scale, keeping the same
    // place in the document in view, and draws the pages near it again.
    function relayout() {
      if (!doc || !entries.length) return;
      const before = scroller.scrollHeight || 1;
      const offset = scroller.scrollTop / before;
      epoch++;
      for (const entry of entries) {
        clearPage(entry);
        sizePage(entry, (entry.page || entries[0].page).getViewport({scale: scale()}));
      }
      scroller.scrollTop = offset * scroller.scrollHeight;
      observe();
    }

    function measureFit() {
      const first = entries[0]?.page;
      if (!first) return false;
      const width = Math.max(120, scroller.clientWidth - 2 * pagePadding);
      const next = width / first.getViewport({scale: 1}).width;
      const changed = Math.abs(next - fitScale) > 0.001;
      fitScale = next;
      return changed;
    }

    (async () => {
      try {
        const pdfjs = await loadLibrary();
        if (destroyed) return;
        task = pdfjs.getDocument({
          url,
          cMapUrl: base + 'cmaps/',
          cMapPacked: true,
          standardFontDataUrl: base + 'standard_fonts/',
          wasmUrl: base + 'wasm/',
          iccUrl: base + 'iccs/',
          isEvalSupported: false,
        });
        task.onPassword = () => {
          report({loading: false, error: 'This PDF is protected by a password.'});
          task.destroy();
        };
        doc = await task.promise;
        if (destroyed) return;
        const first = await doc.getPage(1);
        if (destroyed) return;
        entries = Array.from({length: doc.numPages}, (_, i) => {
          const el = document.createElement('div');
          el.className = 'file-browser-pdf-page';
          el.dataset.page = String(i + 1);
          el.style.setProperty('--scale-round-x', '1px');
          el.style.setProperty('--scale-round-y', '1px');
          return {number: i + 1, el, page: i === 0 ? first : null, drawn: -1, drawing: -1, renderTask: null, textLayer: null};
        });
        measureFit();
        for (const entry of entries) sizePage(entry, first.getViewport({scale: scale()}));
        pagesEl.replaceChildren(...entries.map(entry => entry.el));
        scroller.scrollTop = scrollTop;
        lastWidth = scroller.clientWidth;
        observe();
        scroller.addEventListener('scroll', onScroll, {passive: true});
        resizeObserver = new ResizeObserver(() => {
          if (Math.abs(scroller.clientWidth - lastWidth) < 1) return;
          lastWidth = scroller.clientWidth;
          if (measureFit()) relayout();
        });
        resizeObserver.observe(scroller);
        report({loading: false, pages: doc.numPages, page: currentPage()});
      } catch (err) {
        if (destroyed || state.error) return;
        report({loading: false, error: err?.name === 'InvalidPDFException' ? 'This file is not a readable PDF.' : 'The PDF could not be shown: ' + (err?.message || String(err))});
      }
    })();

    return {
      setZoom(value) {
        const next = clampZoom(value);
        if (next === state.zoom) return;
        report({zoom: next});
        relayout();
      },
      zoomBy(factor) { this.setZoom(state.zoom * factor); },
      get zoom() { return state.zoom; },
      get scrollTop() { return scroller.scrollTop; },
      destroy() {
        if (destroyed) return;
        destroyed = true;
        observer?.disconnect();
        resizeObserver?.disconnect();
        scroller.removeEventListener('scroll', onScroll);
        for (const entry of entries) clearPage(entry);
        entries = [];
        task?.destroy();
      },
    };
  }

  window.KoderPDFPreview = {mount, minZoom, maxZoom};
})();
