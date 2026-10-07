// Sends a zero-value transaction to yourself on Galileo. Needs a funded PRIVATE_KEY.
import { ethers } from 'ethers';
import { RPC_URL, EXPLORER } from './env.mjs';

if (!process.env.PRIVATE_KEY) throw new Error('Set PRIVATE_KEY in .env (npm run wallet)');
const provider = new ethers.JsonRpcProvider(RPC_URL);
const wallet = new ethers.Wallet(process.env.PRIVATE_KEY, provider);

const tx = await wallet.sendTransaction({ to: wallet.address, value: 0n });
console.log('sent', `${EXPLORER}/tx/${tx.hash}`);
const receipt = await tx.wait();
console.log('mined in block', receipt.blockNumber, 'status', receipt.status);
