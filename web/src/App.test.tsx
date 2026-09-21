import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, expect, it } from 'vitest'
import App from './App'
import { server } from './test/server'

// 用同一套组件验证子路径登录、导航与退出,避免只测试字符串拼接。
afterEach(() => {
  document.querySelector('meta[name="hme-base-path"]')?.remove()
  window.history.replaceState(null, '', '/')
})

it('在子路径下登录、读取 API、导航及退出', async () => {
  const meta = document.createElement('meta')
  meta.name = 'hme-base-path'
  meta.content = '/hme/'
  document.head.append(meta)
  window.history.replaceState(null, '', '/hme/accounts')
  let accountRequests = 0
  let logoutCSRF: string | null = null
  server.use(
    http.get('/hme/api/auth/session', () => HttpResponse.json({ success: false }, { status: 401 })),
    http.post('/hme/api/auth/login', () => HttpResponse.json({ success: true, data: { csrf_token: 'mounted-csrf', expires_at: '2030-01-01T00:00:00Z' } })),
    http.get('/hme/api/accounts', () => {
      accountRequests++
      return HttpResponse.json({ success: true, data: [] })
    }),
    http.post('/hme/api/auth/logout', ({ request }) => {
      logoutCSRF = request.headers.get('X-CSRF-Token')
      return HttpResponse.json({ success: true, data: {} })
    }),
  )
  const user = userEvent.setup()
  render(<App />)
  await user.type(await screen.findByLabelText('管理员密码'), 'test-password')
  expect(window.location.pathname).toBe('/hme/login')
  await user.click(screen.getByRole('button', { name: '登录' }))
  const aliases = await screen.findByRole('link', { name: '别名' })
  expect(window.location.pathname).toBe('/hme/accounts')
  await waitFor(() => expect(accountRequests).toBeGreaterThan(0))
  expect(aliases).toHaveAttribute('href', '/hme/aliases')
  await user.click(aliases)
  expect(window.location.pathname).toBe('/hme/aliases')
  await user.click(screen.getByRole('button', { name: '退出登录' }))
  await screen.findByLabelText('管理员密码')
  expect(window.location.pathname).toBe('/hme/login')
  expect(logoutCSRF).toBe('mounted-csrf')
})
