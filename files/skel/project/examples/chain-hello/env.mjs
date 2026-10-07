// Loads .env if present (Node 22 built in, no dotenv needed) and exports config.
try { process.loadEnvFile(new URL('./.env', import.meta.url)); } catch {}
export const RPC_URL = process.env.RPC_URL || 'https://evmrpc-testnet.0g.ai';
export const EXPLORER = 'https://chainscan-galileo.0g.ai';
export const EXPECTED_CHAIN_ID = 16602n;
