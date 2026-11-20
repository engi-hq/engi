/// <reference types="vitest/config" />
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// `npm run dev` proxies the API to a running `engi serve`; override with ENGI_URL.
const engi = process.env.ENGI_URL ?? 'http://localhost:8080';
const proxied = ['/api', '/oauth2', '/hook', '/hook-test', '/hook-wait', '/healthz', '/readyz'];

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: Object.fromEntries(proxied.map((p) => [p, { target: engi, changeOrigin: false }])),
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
    chunkSizeWarningLimit: 600,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('@codemirror') || id.includes('@lezer') || id.includes('style-mod')) return 'codemirror';
          if (id.includes('@xyflow') || id.includes('d3-')) return 'flow';
        },
      },
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: false,
  },
});
