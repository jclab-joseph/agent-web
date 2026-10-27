// The UI is a static SPA embedded into the Go binary; `pnpm build`
// (nuxt generate) writes it to .output/public.
export default defineNuxtConfig({
  compatibilityDate: '2026-10-01',
  ssr: false,
  devtools: { enabled: false },
  css: ['~/assets/main.css', '@xterm/xterm/css/xterm.css'],
  app: {
    head: {
      title: 'agent-web',
      meta: [
        { name: 'viewport', content: 'width=device-width, initial-scale=1, viewport-fit=cover' },
        { name: 'color-scheme', content: 'light dark' },
      ],
      link: [{ rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    },
  },
  nitro: {
    // `pnpm dev` talks to a Go server started with `go run ./cmd/agent-web`.
    devProxy: {
      '/api': { target: process.env.AGENT_WEB_API || 'http://127.0.0.1:8787/api', changeOrigin: false },
    },
  },
  typescript: { strict: true },
})
