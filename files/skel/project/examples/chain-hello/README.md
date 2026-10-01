# chain-hello

Talk to the 0G Galileo testnet (chain id 16602) with ethers v6.

```bash
npm install
npm run hello                 # chain id, latest block, gas price (no key needed)
npm run wallet                # make a throwaway testnet wallet, saved to .env
# fund the printed address at https://faucet.0g.ai (0.1 0G per day), then:
npm run hello                 # now also shows your balance
npm run send                  # zero-value tx to yourself, prints the explorer link
npm run deploy                # compile contracts/Counter.sol (evmVersion cancun), deploy, call increment()
npm run deploy -- --compile-only   # compile only, no key needed
```

Config lives in `.env` (see `.env.example`). Only ever use a throwaway testnet key.
Explorer: https://chainscan-galileo.0g.ai
