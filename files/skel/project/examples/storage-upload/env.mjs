try { process.loadEnvFile(new URL('./.env', import.meta.url)); } catch {}
export const RPC_URL = process.env.RPC_URL || 'https://evmrpc-testnet.0g.ai';
export const INDEXER_RPC = process.env.INDEXER_RPC || 'https://indexer-storage-testnet-turbo.0g.ai';
