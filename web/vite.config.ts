import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyDirOnBuild: false,
  },
  server: {
    proxy: {
      '/auth': 'http://localhost:8374',
      '/stream': 'http://localhost:8374',
      '/input': 'http://localhost:8374',
    },
  },
})
