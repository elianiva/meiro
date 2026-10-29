import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  site: "https://meiro.elianiva.com",
  vite: { plugins: [tailwindcss()] },
});
