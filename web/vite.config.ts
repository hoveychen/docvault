import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev server proxies API + auth + health to the Go server on :8080 so the
// browser keeps a single origin (cookies work without CORS juggling).
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: "http://localhost:8080", changeOrigin: false },
      "/healthz": { target: "http://localhost:8080", changeOrigin: false },
    },
  },
  build: { outDir: "dist" },
});
