import { defineConfig } from 'vite';
import { resolve } from 'path';

export default defineConfig(({ command }) => {
  const isServe = command === 'serve';
  
  return {
    root: 'frontend',
    
    build: isServe ? {} : {
      // SPA build copied into internal/ui/dist and embedded in the binary
      outDir: resolve(__dirname, 'dist'),
      emptyOutDir: true,
    },
    
    resolve: {
      alias: {
        '@': resolve(__dirname, 'frontend')
      }
    },
    
    // Development server configuration
    server: {
      port: 5173,
      host: true,  // Bind to 0.0.0.0 for container/devcontainer access
      // Optional dev-server proxy to the Go API (not used when the binary serves the SPA)
      proxy: {
        '/ui/config': 'http://localhost:8000',
        '/ui/logo': 'http://localhost:8000',
        '/health': 'http://localhost:8000',
        '/metrics': 'http://localhost:8000',
        '/v1': {
          target: 'http://localhost:8000',
          ws: true,
        },
      }
    },
  };
});
