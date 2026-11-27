// Stands in for the vendored slim-select in component tests. The DOM shim
// cannot mount the real library (it reads layout and classList APIs the shim
// does not model), and a constructor that throws leaves preact's effect queue
// stalled for every test after it. This double keeps the surface the Select
// wrapper uses: the constructor, setData, enable/disable, open/close, destroy,
// and the settings and render handles its scroll listener reads. The pick
// protocol itself is covered by select.test.mjs against selectResync.
export default class SlimSelect {
  constructor(config) {
    this.selectEl = config.select;
    this.settings = { isOpen: false, ...config.settings };
    this.render = { content: { main: { contains: () => false } } };
    this.data = [];
  }

  setData(data) { this.data = data; }

  enable() {}

  disable() {}

  open() { this.settings.isOpen = true; }

  close() { this.settings.isOpen = false; }

  destroy() {}
}
