import { defineConfig } from "vite";
import solid from "vite-plugin-solid";

export default defineConfig({
  plugins: [solid()],
  base: "/admin/",
  server: {
    proxy: {
      "/admin/api": {
        target: process.env.ADMIN_DEV_UPSTREAM || "http://localhost:8081",
      },
    },
  },
});
