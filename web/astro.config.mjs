// Static build of the shelf pages; the output is embedded into the Go binary (go/webdist, see go/extra.go).
import { defineConfig } from 'astro/config';
import react from '@astrojs/react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  output: 'static',
  outDir: '../go/webdist',
  build: { format: 'file', inlineStylesheets: 'never' },
  integrations: [react()],
  vite: { plugins: [tailwindcss()] },
  devToolbar: { enabled: false },
  server: { port: 4321 },
});
