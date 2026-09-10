import { mkdirSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineConfig, type Plugin } from 'vitest/config'
import react from '@vitejs/plugin-react'

/**
 * `desktop/main.go` embeds `frontend/dist` and `internal/httpapi` embeds a copy
 * of it, so both need the directory to exist on a fresh checkout. Only the
 * placeholder is committed, and `emptyOutDir` removes it on every build, so put
 * it back once the bundle is written.
 */
function keepDist(outDir: string): Plugin {
  return {
    name: 'sirdar-keep-dist',
    apply: 'build',
    closeBundle() {
      const dir = resolve(__dirname, outDir)
      mkdirSync(dir, { recursive: true })
      writeFileSync(resolve(dir, '.gitkeep'), '')
    },
  }
}

// The bundle is loaded both from the Wails asset server and from the Go
// binary's embedded FS in `sirdar serve`, so every asset URL must be relative.
export default defineConfig({
  base: './',
  plugins: [react(), keepDist('dist')],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts', 'src/**/*.test.tsx'],
  },
})
