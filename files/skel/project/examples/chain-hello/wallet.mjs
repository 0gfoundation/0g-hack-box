// Creates a throwaway testnet wallet and writes it to .env (never overwrites an existing key).
import { ethers } from 'ethers';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';

const envPath = new URL('./.env', import.meta.url);
let env = existsSync(envPath) ? readFileSync(envPath, 'utf8') : readFileSync(new URL('./.env.example', import.meta.url), 'utf8');
const current = env.match(/^PRIVATE_KEY=(0x[0-9a-fA-F]{64})\s*$/m);
if (current) {
  console.log('.env already has a key for', new ethers.Wallet(current[1]).address);
} else {
  const w = ethers.Wallet.createRandom();
  env = /^PRIVATE_KEY=.*$/m.test(env) ? env.replace(/^PRIVATE_KEY=.*$/m, `PRIVATE_KEY=${w.privateKey}`) : env + `\nPRIVATE_KEY=${w.privateKey}\n`;
  writeFileSync(envPath, env, { mode: 0o600 });
  console.log('New throwaway testnet wallet written to .env');
  console.log('Address:', w.address);
}
console.log('Fund it (0.1 0G per day) at https://faucet.0g.ai then run: npm run hello');
