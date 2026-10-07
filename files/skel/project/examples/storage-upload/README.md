# storage-upload

Upload and download files with 0G Storage (`@0gfoundation/0g-storage-ts-sdk`) on Galileo.

```bash
npm install
npm run check                          # merkle root of hello.txt + indexer reachability (no key)
cp .env.example .env                   # then paste a funded throwaway PRIVATE_KEY
npm run upload                         # uploads hello.txt, prints rootHash and txHash
npm run upload -- --text "gm from 0G"  # uploads a string instead of a file
npm run download -- <rootHash> out.txt # anyone can download by root hash, no key
```

Keep the root hash: it is the only handle to your file. Uploads cost a tiny storage fee
plus gas, so the key needs a little testnet 0G (https://faucet.0g.ai).
`ethers` is pinned to 6.13.1 because the storage SDK declares exactly that peer version.
Storage explorer: https://storagescan-galileo.0g.ai
