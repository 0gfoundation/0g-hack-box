// Read-only: computes a file's merkle root locally and asks the indexer for storage nodes.
// Needs no key and no funds. Usage: npm run check -- [path]
import { ZgFile, Indexer } from '@0gfoundation/0g-storage-ts-sdk';
import { INDEXER_RPC } from './env.mjs';

const path = process.argv[2] || new URL('./hello.txt', import.meta.url).pathname;
const file = await ZgFile.fromFilePath(path);
const [tree, treeErr] = await file.merkleTree();
if (treeErr !== null) throw new Error(`merkle tree: ${treeErr}`);
console.log('file      ', path, file.size(), 'bytes');
console.log('root hash ', tree.rootHash());
await file.close();

const indexer = new Indexer(INDEXER_RPC);
const [nodes, err] = await indexer.selectNodes(1);
if (err !== null) throw new Error(`indexer: ${err}`);
console.log('indexer   ', INDEXER_RPC, 'selected', nodes.length, 'storage node(s)');
