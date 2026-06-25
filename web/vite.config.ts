import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    // Dev only: forward API calls to the compose stack (nginx :80), so the
    // browser sees one origin — no CORS. In prod the ingress does this.
    proxy: {
      '/api': 'http://localhost:80',
    },
  },
});
