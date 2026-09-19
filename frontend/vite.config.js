import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

// Allow overriding the backend target for development against a remote
// openmanetd instance:
//   VITE_API_TARGET=http://10.41.1.1:8081 pnpm run dev
const apiTarget = process.env.VITE_API_TARGET || 'http://localhost:8080';

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: './src/test-setup.js',
  },
  // Build output goes into ../static/ so the Go binary can embed it
  build: {
    outDir: path.resolve(__dirname, '../static'),
    emptyOutDir: false,
    // Rolldown's CSS minifier emits media query range syntax — `(width<=640px)`
    // — unless a target constrains it. Browsers older than Chrome 104 /
    // Safari 16.4 discard those at-rules wholesale, which silently disabled
    // every responsive rule in the shipped bundle. These floors cover the
    // stock browsers on the embedded ARM and Android field devices while
    // still permitting custom properties, flexbox gap, and grid.
    // Guarded by frontend/scripts/check-css-target.mjs in `make frontend`.
    cssTarget: ['chrome87', 'safari13.1', 'firefox78', 'edge88'],
    // Raised above the size of the (lazy-loaded) TopologyMap chunk, which
    // carries reagraph + three.js. Any synchronous chunk exceeding this is a
    // genuine regression worth investigating.
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      output: {
        // Vite 8 uses Rolldown by default, which only accepts the function
        // form of manualChunks; the object form is rejected at build time.
        manualChunks: (id) => {
          if (
            id.includes('/node_modules/react/') ||
            id.includes('/node_modules/react-dom/') ||
            id.includes('/node_modules/react-router-dom/')
          ) {
            return 'vendor-react';
          }
          if (
            id.includes('/node_modules/@connectrpc/connect/') ||
            id.includes('/node_modules/@connectrpc/connect-web/') ||
            id.includes('/node_modules/@bufbuild/protobuf/')
          ) {
            return 'vendor-connect';
          }
        },
      },
    },
  },
  server: {
    // Proxy every API surface to the Go frontend daemon during development.
    // /rpc and /auth are themselves reverse-proxied by the frontend daemon
    // through to the ConnectRPC API server, so dev mirrors prod's single-
    // origin model and there is no special-cased rewrite here.
    proxy: {
      '/ws': { target: apiTarget, ws: true },
      '/api': { target: apiTarget, ws: true },
      '/auth': { target: apiTarget },
      '/rpc': { target: apiTarget },
      '/whisper': { target: apiTarget },
    },
  },
});
