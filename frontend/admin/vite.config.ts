import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const allowedHosts = env.VITE_ALLOWED_HOSTS
    ? env.VITE_ALLOWED_HOSTS.split(',').map((host) => host.trim())
    : ['localhost']

  return {
    base: env.VITE_ADMIN_BASE_PATH || '/',
    plugins: [react()],
    server: {
      host: true,
      port: 5174,
      allowedHosts,
      cors: true,
      historyApiFallback: true,
    },
    build: {
      rollupOptions: {
        output: {
          manualChunks: (id) => {
            if (id.includes('node_modules')) return 'vendor'
          },
        },
      },
    },
  }
})
