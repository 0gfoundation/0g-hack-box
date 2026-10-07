# compute-chat

Call an LLM through the 0G Compute Router, an OpenAI-compatible API. No dependencies:
it uses Node's built in `fetch`.

```bash
npm run models                         # live model catalog (no key needed)
npm run chat -- "Explain 0G Storage to a five year old"
```

The key is taken from `ZG_ROUTER_API_KEY` (env or `.env`), otherwise from the key file the
hack box provides. Base URL `OG_ROUTER_URL` (default `https://router-api.0g.ai`), model
`OG_MODEL` (default `glm-5.3`). Import `chat()` from `router.mjs` in your own server code.
Keep the key on the server: never send it to the browser.
