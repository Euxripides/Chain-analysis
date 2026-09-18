import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import App from './App'
import { SessionScopeProvider } from './lib/sessionScope'
import './styles.css'

const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false, retry: 1 } },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      {/* 工作区作用域必须在 QueryClientProvider 之内：作用域变化时清空缓存（§30.1）。 */}
      <SessionScopeProvider>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </SessionScopeProvider>
    </QueryClientProvider>
  </StrictMode>,
)
