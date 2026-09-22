import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { request, setCSRFToken, registerUnauthorizedHandler } from './client'
import { server } from '../test/server'

describe('api client', () => {
  beforeEach(() => {
    setCSRFToken(null)
    registerUnauthorizedHandler(null)
    server.resetHandlers()
  })

  it('成功解包 data', async () => {
    server.use(
      http.get('/api/accounts', () =>
        HttpResponse.json({ success: true, data: { id: 'acc_1' } }),
      ),
    )
    const data = await request<{ id: string }>('/api/accounts')
    expect(data.id).toBe('acc_1')
  })

  it('非 2xx 抛出 ApiError 并携带 code/message/status', async () => {
    server.use(
      http.get('/api/accounts', () =>
        HttpResponse.json(
          { success: false, code: 'VALIDATION_ERROR', message: '参数错误' },
          { status: 400 },
        ),
      ),
    )
    await expect(request('/api/accounts')).rejects.toMatchObject({
      status: 400,
      code: 'VALIDATION_ERROR',
      message: '参数错误',
    })
  })

  it('非 JSON 网关错误保留 HTTP 状态并提示重试', async () => {
    server.use(
      http.get('/api/accounts', () =>
        new HttpResponse('<html>bad</html>', { status: 502 }),
      ),
    )
    await expect(request('/api/accounts')).rejects.toMatchObject({ status: 502, code: 'GATEWAY_ERROR', message: '服务暂时不可用（HTTP 502），请稍后重试' })
  })

  it('401 触发全局回调', async () => {
    const onUnauthorized = vi.fn()
    server.use(
      http.get('/api/accounts', () =>
        HttpResponse.json(
          { success: false, code: 'AUTH_REQUIRED', message: '请先登录' },
          { status: 401 },
        ),
      ),
    )
    request('/api/accounts', undefined, onUnauthorized).catch(() => {})
    await vi.waitFor(() => expect(onUnauthorized).toHaveBeenCalled())
  })

  it('iCloud 会话失效提示更新凭据，但不触发管理台退出', async () => {
    const onUnauthorized = vi.fn()
    const globalUnauthorized = vi.fn()
    registerUnauthorizedHandler(globalUnauthorized)
    server.use(
      http.get('/api/aliases', () => HttpResponse.json(
        { success: false, code: 'UPSTREAM_UNAUTHORIZED', message: 'iCloud 会话已失效，请到「账号」更新 Cookie 或重新登录 iCloud' },
        { status: 401 },
      )),
    )
    await expect(request('/api/aliases', undefined, onUnauthorized)).rejects.toMatchObject({
      status: 401, code: 'UPSTREAM_UNAUTHORIZED', message: expect.stringContaining('更新 Cookie'),
    })
    expect(onUnauthorized).not.toHaveBeenCalled()
    expect(globalUnauthorized).not.toHaveBeenCalled()
    registerUnauthorizedHandler(null)
  })

  it.each(['OTP_INVALID', 'ICLOUD_LOGIN_REJECTED'])('iCloud 登录错误 %s 不退出管理台', async (code) => {
    const local = vi.fn()
    const global = vi.fn()
    registerUnauthorizedHandler(global)
    server.use(http.post('/api/accounts/acc_1/login', () => HttpResponse.json(
      { success: false, code, message: '请检查登录信息后重试' }, { status: 401 },
    )))
    await expect(request('/api/accounts/acc_1/login', { method: 'POST' }, local)).rejects.toMatchObject({ status: 401, code })
    expect(local).not.toHaveBeenCalled()
    expect(global).not.toHaveBeenCalled()
  })

  it('Cloudflare 错误 JSON 不使用模糊提示或泄露代理细节', async () => {
    server.use(http.get('/api/aliases', () => HttpResponse.json(
      { type: 'about:blank', title: 'Bad Gateway', status: 502, detail: 'private upstream details' },
      { status: 502 },
    )))
    await expect(request('/api/aliases')).rejects.toMatchObject({
      status: 502, code: 'GATEWAY_ERROR', message: '服务暂时不可用（HTTP 502），请稍后重试',
    })
  })

  it('非 JSON 的 401 仍触发管理台退出', async () => {
    const onUnauthorized = vi.fn()
    server.use(http.get('/api/accounts', () => new HttpResponse('Unauthorized', { status: 401 })))
    await expect(request('/api/accounts', undefined, onUnauthorized)).rejects.toMatchObject({ status: 401 })
    expect(onUnauthorized).toHaveBeenCalledOnce()
  })

  it.each([null, {}, [], 'unexpected'])('拒绝无效的成功响应 %j', async (body) => {
    server.use(http.get('/api/accounts', () => HttpResponse.json(body)))
    await expect(request('/api/accounts')).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
  })

  it('GET 不带 CSRF,POST 自动带 CSRF', async () => {
    setCSRFToken('csrf-token-123')
    let getHeaders: Headers | undefined
    let postHeaders: Headers | undefined
    server.use(
      http.get('/api/auth/session', ({ request }) => {
        getHeaders = request.headers
        return HttpResponse.json({ success: true, data: {} })
      }),
      http.post('/api/auth/logout', ({ request }) => {
        postHeaders = request.headers
        return HttpResponse.json({ success: true, data: { logged_out: true } })
      }),
    )
    await request('/api/auth/session')
    await request('/api/auth/logout', { method: 'POST' })
    expect(getHeaders?.get('X-CSRF-Token')).toBeNull()
    expect(postHeaders?.get('X-CSRF-Token')).toBe('csrf-token-123')
  })

  it('使用 credentials same-origin', async () => {
    let seen: RequestInit | undefined
    server.use(
      http.get('/api/accounts', ({ request }) => {
        seen = request as unknown as RequestInit
        return HttpResponse.json({ success: true, data: [] })
      }),
    )
    await request('/api/accounts')
    expect(seen?.credentials).toBe('same-origin')
  })

  it('支持 AbortSignal', async () => {
    const controller = new AbortController()
    server.use(
      http.get('/api/accounts', () =>
        HttpResponse.json({ success: true, data: [] }),
      ),
    )
    controller.abort()
    await expect(
      request('/api/accounts', { signal: controller.signal }),
    ).rejects.toThrow()
  })
})
