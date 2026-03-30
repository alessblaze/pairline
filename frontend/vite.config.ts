import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  
  const allowedHosts = env.VITE_ALLOWED_HOSTS ? env.VITE_ALLOWED_HOSTS.split(',').map(s => s.trim()) : ['localhost']

  return {
    plugins: [react()],
    server: {
      host: true,
      port: 5173,
      allowedHosts: allowedHosts,
      cors: true,
      historyApiFallback: true,
    },
  }
})
