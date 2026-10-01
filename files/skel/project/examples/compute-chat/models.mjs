// Lists the Router's live model catalog. No key needed.
import { BASE } from './router.mjs';
const { data } = await (await fetch(`${BASE}/models`)).json();
const rows = data
  .filter((m) => (m.provider_count ?? 0) > 0 && (m.supported_parameters ?? []).includes('tools'))
  .map((m) => `${m.id.padEnd(28)} providers=${m.provider_count}  ctx=${m.context_length}`);
console.log(`${data.length} models, ${rows.length} with tool calling:\n` + rows.join('\n'));
