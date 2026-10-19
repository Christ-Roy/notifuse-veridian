import ReactDOM from 'react-dom/client'
import App from './App'
import 'antd/dist/reset.css'
import './index.css'
// === Veridian patch === Install global fetch interceptor avant le boot
// React pour capturer toutes les réponses backend (paywall 402, hub_sync_dead
// 503, soft-delete headers). Idempotent. Cf. veridian_402_interceptor.ts.
import { installChunkReloadGuard } from './lib/chunkReload'
import { installVeridianFetchInterceptor } from './services/api/veridian_402_interceptor'

installVeridianFetchInterceptor()
// Chunk perime apres un deploiement : un rechargement automatique (cf. lib/chunkReload.ts).
installChunkReloadGuard()

ReactDOM.createRoot(document.getElementById('root')!).render(<App />)
