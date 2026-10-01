// Compiles contracts/Counter.sol with solc-js (evmVersion cancun, as the 0G docs require),
// deploys it to Galileo and calls increment(). Needs a funded PRIVATE_KEY.
// Pass --compile-only to just compile (no key, no funds).
import { ethers } from 'ethers';
import solc from 'solc';
import { readFileSync } from 'node:fs';
import { RPC_URL, EXPLORER } from './env.mjs';

const source = readFileSync(new URL('./contracts/Counter.sol', import.meta.url), 'utf8');
const input = {
  language: 'Solidity',
  sources: { 'Counter.sol': { content: source } },
  settings: {
    evmVersion: 'cancun',
    optimizer: { enabled: true, runs: 200 },
    outputSelection: { '*': { '*': ['abi', 'evm.bytecode.object'] } },
  },
};
const out = JSON.parse(solc.compile(JSON.stringify(input)));
const errors = (out.errors || []).filter((e) => e.severity === 'error');
if (errors.length) throw new Error(errors.map((e) => e.formattedMessage).join('\n'));
const { abi, evm } = out.contracts['Counter.sol'].Counter;
console.log('compiled with solc', solc.version(), 'evmVersion cancun, bytecode', evm.bytecode.object.length / 2, 'bytes');
if (process.argv.includes('--compile-only')) process.exit(0);

if (!process.env.PRIVATE_KEY) throw new Error('Set PRIVATE_KEY in .env (npm run wallet)');
const wallet = new ethers.Wallet(process.env.PRIVATE_KEY, new ethers.JsonRpcProvider(RPC_URL));
const factory = new ethers.ContractFactory(abi, evm.bytecode.object, wallet);
const counter = await factory.deploy();
await counter.waitForDeployment();
const address = await counter.getAddress();
console.log('deployed', `${EXPLORER}/address/${address}`);

const tx = await counter.increment();
await tx.wait();
console.log('number() =', (await counter.number()).toString());
