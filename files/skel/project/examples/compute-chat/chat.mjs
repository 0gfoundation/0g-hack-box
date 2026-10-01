// One-shot chat through the 0G Compute Router. Usage: npm run chat -- "your prompt"
import { chat, MODEL } from './router.mjs';

const prompt = process.argv.slice(2).join(' ') || 'In one sentence, what is 0G?';
const res = await chat(
  [
    { role: 'system', content: 'You are a concise assistant.' },
    { role: 'user', content: prompt },
  ],
  { max_tokens: 512, chat_template_kwargs: { enable_thinking: false } }, // GLM-5: skip thinking for speed
);
console.log(res.choices[0].message.content);
const t = res.x_0g_trace;
if (t) console.error(`\n[${MODEL} via provider ${t.provider}, cost ${Number(t.billing?.total_cost ?? 0) / 1e18} 0G]`);
