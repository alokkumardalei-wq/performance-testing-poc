import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react-swc';

// Lib-mode ES build: one dist/main.js the OpenEverest host dynamic-import()s.
// No externals — the plugin bundles its own React/MUI and mounts its own root
// inside a host-provided div (micro-frontend pattern), so the host's React
// version never collides with ours.
export default defineConfig({
  plugins: [react()],
  define: { 'process.env.NODE_ENV': '"production"' },
  build: {
    lib: { entry: 'src/main.tsx', formats: ['es'], fileName: () => 'main.js' },
  },
  server: {
    port: 3001,
    cors: true,
    proxy: {
      // Dev sandbox: plugin API served by the local backend.
      '/api': { target: 'http://127.0.0.1:8081', changeOrigin: true },
    },
  },
});
