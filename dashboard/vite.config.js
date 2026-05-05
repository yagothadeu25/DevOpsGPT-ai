import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  root: ".",
  server: {
    host: "0.0.0.0",
    port: 3000,
    proxy: {
      "/v1": "http://devopsgpt:8080",
      "/healthz": "http://devopsgpt:8080",
    },
  },
});
