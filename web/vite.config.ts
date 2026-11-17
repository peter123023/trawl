import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Built assets land inside the Go package so //go:embed can pick them up.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:8080",
      "/v1": "http://localhost:8080",
    },
  },
});
