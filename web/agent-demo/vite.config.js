import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

const apiTarget = process.env.KIMO_API_TARGET || "http://127.0.0.1:8888";
const proxy = {
  "/agent": apiTarget,
  "/pptx": apiTarget,
  "/session": apiTarget,
  "/trace": apiTarget,
  "/healthz": apiTarget
};

export default defineConfig({
  plugins: [vue()],
  server: {
    host: "127.0.0.1",
    port: 5173,
    proxy
  },
  preview: {
    host: "127.0.0.1",
    port: 4173,
    proxy
  },
  build: {
    outDir: "dist",
    emptyOutDir: true
  }
});
