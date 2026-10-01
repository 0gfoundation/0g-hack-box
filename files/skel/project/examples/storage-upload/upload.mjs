// Uploads a file (or a string) to 0G Storage. Needs a funded testnet PRIVATE_KEY.
// Usage: npm run upload -- [path]     (defaults to hello.txt)
//        npm run upload -- --text "any string"
import { ZgFile, MemData, Indexer } from '@0gfoundation/0g-storage-ts-sdk';
import { ethers } from 'ethers';
import { RPC_URL, INDEXER_RPC } from './env.mjs';

if (!process.env.PRIVATE_KEY) throw new Error('Set PRIVATE_KEY in .env (a funded throwaway testnet key)');
const signer = new ethers.Wallet(process.env.PRIVATE_KEY, new ethers.JsonRpcProvider(RPC_URL));
const indexer = new Indexer(INDEXER_RPC); // flow contract is discovered from the indexer

const i = process.argv.indexOf('--text');
const data = i > -1
  ? new MemData(new TextEncoder().encode(process.argv[i + 1] ?? ''))
  : await ZgFile.fromFilePath(process.argv[2] || new URL('./hello.txt', import.meta.url).pathname);

const [tree, treeErr] = await data.merkleTree(); // must run before upload
if (treeErr !== null) throw new Error(`merkle tree: ${treeErr}`);
console.log('root hash', tree.rootHash());

const [tx, err] = await indexer.upload(data, RPC_URL, signer);
if (data.close) await data.close();
if (err !== null) throw new Error(`upload: ${err}`);
if ('rootHash' in tx) console.log('uploaded', { rootHash: tx.rootHash, txHash: tx.txHash });
else console.log('uploaded (fragmented)', tx);
console.log('Download it with: npm run download --', 'rootHash' in tx ? tx.rootHash : tx.rootHashes[0]);
