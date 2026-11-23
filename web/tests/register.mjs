// Preloaded with `node --import`: lets the WebUI's own modules, which import
// each other and the vendored libraries by absolute URL path (/vendor/...),
// load under Node.
import { register } from 'node:module';

register('./loader.mjs', import.meta.url);
