// Read-only: chain id, latest block, gas price and a balance. Needs no funds.
import { ethers } from 'ethers';
import { RPC_URL, EXPECTED_CHAIN_ID } from './env.mjs';

const provider = new ethers.JsonRpcProvider(RPC_URL);
const net = await provider.getNetwork();
console.log('RPC        ', RPC_URL);
console.log('chain id   ', net.chainId.toString(), net.chainId === EXPECTED_CHAIN_ID ? '(Galileo testnet)' : '(not Galileo!)');

const block = await provider.getBlock('latest');
console.log('block      ', block.number, new Date(block.timestamp * 1000).toISOString());

const fee = await provider.getFeeData();
console.log('gas price  ', ethers.formatUnits(fee.gasPrice ?? 0n, 'gwei'), 'gwei');

let address = process.env.ADDRESS;
if (!address && process.env.PRIVATE_KEY) address = new ethers.Wallet(process.env.PRIVATE_KEY).address;
if (address) {
  const bal = await provider.getBalance(address);
  console.log('balance    ', address, ethers.formatEther(bal), '0G');
} else {
  console.log('balance     (set ADDRESS or PRIVATE_KEY in .env, or run: npm run wallet)');
}
