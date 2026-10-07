// Downloads a file from 0G Storage by merkle root, with proof verification. No key needed.
// Usage: npm run download -- <rootHash> [outPath]
import { Indexer } from '@0gfoundation/0g-storage-ts-sdk';
import { existsSync } from 'node:fs';
import { INDEXER_RPC } from './env.mjs';

const [root, out = 'downloaded.bin'] = process.argv.slice(2);
if (!root) throw new Error('Usage: npm run download -- <rootHash> [outPath]');
if (existsSync(out)) throw new Error(`${out} already exists, pick another path`);
const indexer = new Indexer(INDEXER_RPC);
const err = await indexer.download(root, out, true);
if (err !== null) throw new Error(`download: ${err}`);
console.log('saved', out);
