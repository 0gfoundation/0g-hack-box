# Building on 0G in 30 minutes

You are helping a hackathon attendee at a shared kiosk. They have **30 minutes** in this
directory (`~/project`) and then the machine is wiped. Optimise for a working demo, fast.
Facts below come from docs.0g.ai (checked 2026-09-30). Do not invent other endpoints.

## What 0G is
0G is an AI-focused, EVM-compatible layer 1 plus decentralized storage, compute and data
availability. Anything that works on Ethereum (ethers, viem, Solidity, Hardhat, Foundry)
works on 0G Chain with a different RPC URL. The native gas token is **0G** (18 decimals).

Building blocks:
- **Chain**: EVM smart contracts and transactions. Use for ownership, registries, payments, public state.
- **Storage**: content-addressed file storage (upload returns a merkle root hash). Use for files, JSON blobs, datasets, anything too big for chain state.
- **Compute**: decentralized LLM inference behind an OpenAI-compatible API (the Router). Use for any AI feature.
- **DA**: data availability for rollups. Not a 30 minute target; skip it.

## Networks
| | Galileo testnet (use this) | Mainnet |
|---|---|---|
| Chain id | `16602` (hex `0x40DA`) | `16661` (hex `0x4115`) |
| RPC | `https://evmrpc-testnet.0g.ai` | `https://evmrpc.0g.ai` |
| Explorer | `https://chainscan-galileo.0g.ai` | `https://chainscan.0g.ai` |
| Storage indexer (turbo) | `https://indexer-storage-testnet-turbo.0g.ai` | `https://indexer-storage-turbo.0g.ai` |
| Storage explorer | `https://storagescan-galileo.0g.ai` | |
| Faucet | `https://faucet.0g.ai`, 0.1 0G per wallet per day | none (real money) |

- **Build everything on Galileo testnet.** Never touch mainnet funds here.
- The public RPCs are for development; they are fine for a hackathon.
- Faucet alternative: `https://cloud.google.com/application/web3/faucet/0g/galileo`.
- 0.1 0G covers many transactions and small uploads (observed gas price about 4 gwei).
- The indexer URL is an API base: `GET /` returns 404 and that is normal.

## House rules for this machine
- No sudo, no global installs (`npm i -g` fails). Install per project with `npm install`, run with `npx` or npm scripts.
- Node 22 and npm are installed. Prefer plain Node ESM (`.mjs`) or TypeScript run with `npx tsx file.ts`. Keep dependencies small; skip big frameworks unless asked.
- Any server binds to `localhost` (for example `http://localhost:3000`); open it in Chromium on this machine.
- **Never ask the human for a private key or seed phrase.** Generate a throwaway testnet wallet (below), save it to `.env`, and tell them to fund the printed address at the faucet. `.env` is gitignored; keep it that way.
- Secrets live in `.env` and are read on the server only. Never put a key in browser code.
- Get something running in the first 10 minutes, then iterate. Tell the human the time budget when you plan.

## Ready-made starters (run these first)
All in `examples/`, each with `npm install` then npm scripts, all tested against Galileo:
- `examples/chain-hello`: `npm run hello` (read chain), `npm run wallet` (throwaway key into `.env`), `npm run send`, `npm run deploy` (solc-js compile with cancun + ethers deploy).
- `examples/storage-upload`: `npm run check` (no key), `npm run upload`, `npm run download -- <root> out`.
- `examples/compute-chat`: `npm run models` (no key), `npm run chat -- "prompt"`. Zero dependencies. The box already provides a Router key.

Copy code out of these rather than starting from scratch.

## Chain: connect, wallet, transaction (ethers v6)
```bash
npm install ethers@6.13.1
```
Use `ethers@6.13.1` everywhere: the storage SDK declares exactly that peer version, and a newer ethers makes `npm install` of the SDK fail with ERESOLVE.
```js
import { ethers } from 'ethers';
const provider = new ethers.JsonRpcProvider('https://evmrpc-testnet.0g.ai');
console.log((await provider.getNetwork()).chainId); // 16602n
console.log(await provider.getBlockNumber());

// Throwaway wallet: print the address, store the key in .env, never log it again.
const fresh = ethers.Wallet.createRandom(); // fresh.address, fresh.privateKey

const wallet = new ethers.Wallet(process.env.PRIVATE_KEY, provider);
console.log(ethers.formatEther(await provider.getBalance(wallet.address)), '0G');
const tx = await wallet.sendTransaction({ to: wallet.address, value: 0n });
await tx.wait();
console.log(`https://chainscan-galileo.0g.ai/tx/${tx.hash}`);
```
Load `.env` without dotenv: `process.loadEnvFile('.env')` (Node 22), or `node --env-file=.env app.mjs`.

Browser wallets: `wallet_addEthereumChain` with `chainId: '0x40DA'`, `chainName: '0G Galileo Testnet'`,
`nativeCurrency: { name: '0G', symbol: '0G', decimals: 18 }`, `rpcUrls: ['https://evmrpc-testnet.0g.ai']`,
`blockExplorerUrls: ['https://chainscan-galileo.0g.ai']`. The kiosk browser has no wallet
extension, so prefer a server-side throwaway key for demos.

## Contracts: compile and deploy
**The 0G docs require `evmVersion: "cancun"`.** Set it in every compiler config.

Fastest path, no framework (see `examples/chain-hello/deploy.mjs`):
```bash
npm install ethers@6.13.1 solc
```
```js
import solc from 'solc';
const input = { language: 'Solidity', sources: { 'C.sol': { content: source } },
  settings: { evmVersion: 'cancun', optimizer: { enabled: true, runs: 200 },
    outputSelection: { '*': { '*': ['abi', 'evm.bytecode.object'] } } } };
const out = JSON.parse(solc.compile(JSON.stringify(input)));
const { abi, evm } = out.contracts['C.sol'].MyContract;
const c = await new ethers.ContractFactory(abi, evm.bytecode.object, wallet).deploy();
await c.waitForDeployment();
console.log(await c.getAddress());
```
Hardhat: the docs show Hardhat 2 style config, but `npm i -D hardhat` now installs Hardhat 3,
whose config format differs, and it is a heavy install. Prefer the solc-js path above. If the
human insists, keep these values: compiler `evmVersion: "cancun"`, network url
`https://evmrpc-testnet.0g.ai`, chainId `16602`, key from `process.env.PRIVATE_KEY`. With ethers v6
use `waitForDeployment()` and `getAddress()`, not the docs' `deployed()` and `.address`.
Foundry, only if `forge` is already installed: `evm_version = "cancun"` in `foundry.toml`, then
`forge create --rpc-url https://evmrpc-testnet.0g.ai --private-key $PRIVATE_KEY --evm-version cancun src/C.sol:C --broadcast`
(Foundry 1.x only simulates without `--broadcast`; the docs omit it).
Contract verification API: `https://chainscan-galileo.0g.ai/open/api`. Optional; skip it under time pressure.

## Storage: upload and download (TypeScript SDK)
```bash
npm install @0gfoundation/0g-storage-ts-sdk ethers@6.13.1
```
```js
import { ZgFile, MemData, Indexer } from '@0gfoundation/0g-storage-ts-sdk';
import { ethers } from 'ethers';
const RPC_URL = 'https://evmrpc-testnet.0g.ai';
const indexer = new Indexer('https://indexer-storage-testnet-turbo.0g.ai'); // flow contract auto-discovered
const signer = new ethers.Wallet(process.env.PRIVATE_KEY, new ethers.JsonRpcProvider(RPC_URL));

const file = await ZgFile.fromFilePath('./note.txt'); // or new MemData(new TextEncoder().encode(str))
const [tree, treeErr] = await file.merkleTree();       // must run before upload
if (treeErr !== null) throw new Error(String(treeErr));
const [tx, err] = await indexer.upload(file, RPC_URL, signer);
await file.close();
if (err !== null) throw new Error(String(err));
console.log(tx.rootHash, tx.txHash); // files over 4GB return rootHashes / txHashes instead

const dlErr = await indexer.download(tx.rootHash, './copy.txt', true); // true = verify merkle proof
```
- The SDK returns `[result, error]` tuples; check the error, it does not throw.
- The root hash is the only handle to a file. Store it (on chain, in your app state) and show it.
- Upload needs a funded key (tiny storage fee plus gas). Download needs no key.
- `indexer.download()` writes to disk (Node only; it refuses to overwrite an existing file). In a web app, download on the server and send bytes to the browser.
- Client-side encryption exists (`indexer.upload(file, RPC_URL, signer, { encryption: { type: 'aes256', key } })`, read back with `indexer.downloadToBlob(root, { proof: true, decryption: { symmetricKey: key } })`).
- Skip 0G KV storage in a 30 minute slot: it needs a KV node and can lag after writes.

## Compute: call an LLM through the 0G Compute Router
The Router is OpenAI-compatible. Base URL `https://router-api.0g.ai/v1`, header
`Authorization: Bearer sk-...`. **This machine already has a credit-limited Router key**:
`examples/compute-chat/router.mjs` finds it (env `ZG_ROUTER_API_KEY`, else the box key file).
Reuse that `apiKey()` helper. Do not ask the human for a key unless the box key is missing.
```js
// zero dependency (Node 22 fetch)
const res = await fetch('https://router-api.0g.ai/v1/chat/completions', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${key}` },
  body: JSON.stringify({ model: 'glm-5.3', messages: [{ role: 'user', content: 'Hello!' }],
    max_tokens: 512, chat_template_kwargs: { enable_thinking: false } }),
});
const data = await res.json(); // data.choices[0].message.content, data.x_0g_trace.billing
```
```js
// or the OpenAI SDK: npm install openai
import OpenAI from 'openai';
const client = new OpenAI({ baseURL: 'https://router-api.0g.ai/v1', apiKey: key });
const r = await client.chat.completions.create({ model: 'glm-5.3', messages: [{ role: 'user', content: 'Hello!' }] });
```
- Model list: `GET https://router-api.0g.ai/v1/models` (no auth). Good defaults: `glm-5.3` (tools, JSON mode, 1M context); `glm-5.3-flash` is cheaper. The docs' examples use `glm-5.2`, also live.
- Supports `stream: true` (OpenAI SSE), `tools`, `response_format: { type: 'json_object' }`. Check a model's `supported_parameters` first: sending `tools` to a model without it returns 400.
- GLM-5 models think by default; `chat_template_kwargs: { enable_thinking: false }` saves seconds and tokens. The reasoning text, when present, is in `message.reasoning_content`.
- Images: `POST /v1/images/generations` with `"response_format": "b64_json"`. Audio: `POST /v1/audio/transcriptions`.
- Errors: 401 bad key, 402 balance empty, 429 rate limited (honor `Retry-After`, do not loop), 503 no provider for that model (pick another model).
- The key is billed in real 0G. Call the Router only from your server, never from browser code, and keep `max_tokens` modest.

## How to finish in 30 minutes
Minute 0 to 10: get one example running and wire it into a tiny app. Minute 10 to 25: add
the one feature that makes the demo. Minute 25 to 30: README, screenshot, save the work.
Web apps: a single `server.mjs` using `node:http` that serves one `index.html` and a couple
of JSON routes (`/api/...`) is enough. No build step.

1. **AI note vault** (Compute + Storage). Type a note, the LLM turns it into a title,
   summary and tags as JSON, the JSON is uploaded to 0G Storage, the page lists root hashes
   and can fetch any note back. Steps: copy `router.mjs` and the upload code into
   `server.mjs`; route `POST /api/note` (LLM then upload); route `GET /api/note/:root`
   (download to a temp file, return it); plain HTML form.
2. **On-chain guestbook with an AI moderator** (Chain + Compute). A `Guestbook` contract
   with `sign(string)` and an event; the server asks the LLM to reject or clean up each
   message, then sends the transaction from the throwaway wallet; the page lists entries
   from events with explorer links. Steps: adapt `Counter.sol` and `deploy.mjs`; save the
   address to `.env`; `POST /api/sign`; `GET /api/entries` via `contract.queryFilter`.
3. **Proof of file** (Storage + Chain). Upload any file to 0G Storage, then record its root
   hash, name and timestamp in a `Registry` contract (`register(bytes32 root, string name)`),
   so anyone can prove the file existed and fetch it by hash. Optional: an LLM caption of
   the file. Steps: deploy the registry; `POST /api/upload` (upload then register);
   `GET /api/files` reads events; a verify box that downloads by root.

## Keep your work (the machine is wiped at 0:00)
Tell the human this early and again with 5 minutes left.
- GitHub (preferred): `git init && git add -A && git commit -m "0G hack"`, then
  `gh auth login --hostname github.com --git-protocol https --web` (device code: they type
  the code shown in the terminal at github.com/login/device in the browser), then
  `gh repo create <name> --public --source=. --push`. If `gh` is missing, create an empty
  repo in the browser and push over HTTPS instead.
- USB stick: plug it in, then `rsync -a --exclude node_modules ~/project/ /media/$USER/<STICK>/project/`.
- Check `.gitignore` covers `.env` and `node_modules` before pushing. The wallet is throwaway either way.

## Pitfalls seen in the docs and in testing
- Chain id is 16602 on Galileo. Older testnets are gone; ignore any other testnet chain id or RPC you remember.
- Compile with `evmVersion: "cancun"`. Docs advise this, and if you see "invalid opcode" also try solc 0.8.19.
- "insufficient funds" means the faucet step was skipped; the fix is funding, not gas tuning. If a tx is stuck or underpriced, pass an explicit `gasPrice` from `provider.getFeeData()`.
- Storage SDK: call `merkleTree()` before `upload()`; close `ZgFile`; use the turbo indexer URL above; check the returned error.
- Storage SDK in the browser needs Node polyfills. Do storage on the server instead.
- Router: rate limits are per account and this box shares one; handle 429 with `Retry-After`. `/v1/account/*` needs an `mk-` management key, not `sk-`.
- Docs pages that show `deployed()` / `token.address` are ethers v5 style; with ethers v6 use `waitForDeployment()` / `getAddress()`.

## Docs
https://docs.0g.ai (testnet: /developer-hub/testnet/testnet-overview, storage SDK:
/developer-hub/building-on-0g/storage/sdk, Router: /developer-hub/building-on-0g/compute-network/router/overview,
contracts: /developer-hub/building-on-0g/contracts-on-0g/deploy-contracts).
