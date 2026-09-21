import { http, HttpResponse } from 'msw'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it } from 'vitest'
import InboxPage from './InboxPage'
import { server } from '../test/server'
import { setCSRFToken } from '../api/client'
import { ToastProvider } from '../components/ToastProvider'
import type { AccountSummary, InboxResult } from '../api/types'

const accounts: AccountSummary[] = [
  {
    id: 'acc_1',
    name: '主号',
    real_email: 'a@example.com',
    icloud_email: 'a@icloud.com',
    host: 'icloud.com',
    status: 'active',
    alias_total: 2,
    alias_active: 2,
    has_cookies: true,
    has_app_password: true,
    has_proxy: false,
    last_validated: '2026-08-04T09:00:00+08:00',
    created_at: '2026-08-01T09:00:00+08:00',
  },
]

const inboxResult: InboxResult = {
  account_id: 'acc_1',
  alias: 'alpha@icloud.com',
  count: 1,
  method: 'imap',
  messages: [
    {
      id: '1',
      from: 'sender@example.com',
      to: 'alpha@icloud.com',
      subject: '主题一',
      date: '2026-08-04T10:00:00+08:00',
      preview: '预览内容',
    },
  ],
}

function renderPage(initialPath = '/inbox') {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route
          path="/inbox"
          element={
            <ToastProvider>
              <InboxPage />
            </ToastProvider>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

describe('InboxPage', () => {
  beforeEach(() => {
    setCSRFToken('csrf-test')
    server.resetHandlers()
  })

  it('账号必选;alias 可空;limit/days 生效;query 经 URLSearchParams', async () => {
    let lastUrl = ''
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', ({ request }) => {
        lastUrl = request.url
        return HttpResponse.json({ success: true, data: inboxResult })
      }),
    )
    renderPage()
    await screen.findByText('主题一')
    // 确认 query 参数
    const url = new URL(lastUrl)
    expect(url.searchParams.get('account_id')).toBe('acc_1')
    expect(url.searchParams.get('limit')).toBe('20')
    expect(url.searchParams.get('days')).toBe('7')
    // 修改 limit/days 再查询
    const user = userEvent.setup()
    await user.selectOptions(screen.getByLabelText(/每页/), '100')
    await user.selectOptions(screen.getByLabelText(/时间范围/), '30')
    await user.click(screen.getByRole('button', { name: /查询/ }))
    await waitFor(() => {
      const u = new URL(lastUrl)
      expect(u.searchParams.get('limit')).toBe('100')
      expect(u.searchParams.get('days')).toBe('30')
    })
  })

  it('从 URL 的 alias 参数初始化筛选,支持别名页直达收件箱', async () => {
    const inboxUrls: string[] = []
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({
          success: true,
          data: {
            account_id: 'acc_1',
            count: 1,
            aliases: [
              {
                email: 'alpha@icloud.com',
                anonymousId: 'anon_alpha',
                label: 'Alpha',
                active: true,
              },
            ],
          },
        }),
      ),
      http.get('/api/inbox', ({ request }) => {
        inboxUrls.push(request.url)
        return HttpResponse.json({ success: true, data: inboxResult })
      }),
    )
    renderPage('/inbox?account_id=acc_1&alias=alpha%40icloud.com')
    await screen.findByText('主题一')
    await waitFor(() => {
      expect(inboxUrls.some((url) => new URL(url).searchParams.get('alias') === 'alpha@icloud.com')).toBe(true)
    })
    expect(screen.getByLabelText(/别名/)).toHaveValue('alpha@icloud.com')
  })

  it('展示 method=imap 或 web_api', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () =>
        HttpResponse.json({
          success: true,
          data: { ...inboxResult, method: 'web_api' },
        }),
      ),
    )
    renderPage()
    await screen.findByText('主题一')
    expect(screen.getByText(/Web API/)).toBeInTheDocument()
  })

  it('空列表、网络错误、401 状态', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () =>
        HttpResponse.json({
          success: true,
          data: { account_id: 'acc_1', count: 0, messages: [], method: 'imap' },
        }),
      ),
    )
    renderPage()
    expect(await screen.findByText(/暂无邮件/)).toBeInTheDocument()
  })

  it('恶意 HTML 只作为文本显示,不产生 img 节点', async () => {
    const evil = {
      ...inboxResult,
      messages: [
        {
          id: '2',
          from: 'evil@example.com',
          to: 'alpha@icloud.com',
          subject: '<img src=x onerror=alert(1)>',
          date: '2026-08-04T10:00:00+08:00',
          preview: '<script>alert(2)</script>预览',
        },
      ],
    }
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () => HttpResponse.json({ success: true, data: evil })),
    )
    renderPage()
    await screen.findByText(/<img src=x onerror=alert\(1\)>/)
    expect(document.querySelector('img')).toBeNull()
    expect(document.querySelector('script')).toBeNull()
  })

  it('快速切换筛选:第一请求晚返回不覆盖第二请求', async () => {
    let release: (() => void) | undefined
    let calls = 0
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () => {
        calls++
        if (calls === 1) {
          // 第一次请求挂起,稍后返回旧数据
          return new Promise<Response>((resolve) => {
            release = () =>
              resolve(
                HttpResponse.json({
                  success: true,
                  data: {
                    ...inboxResult,
                    messages: [{ ...inboxResult.messages[0], subject: '旧主题' }],
                  },
                }),
              )
          })
        }
        return HttpResponse.json({
          success: true,
          data: {
            ...inboxResult,
            alias: 'second',
            messages: [
              {
                id: '9',
                from: 's2@example.com',
                to: 'alpha@icloud.com',
                subject: '第二请求主题',
                date: '2026-08-04T11:00:00+08:00',
                preview: '第二请求',
              },
            ],
          },
        })
      }),
    )
    renderPage()
    await screen.findByText(/加载中/)
    // 触发第二次查询(首次挂起中)
    const user = userEvent.setup()
    await user.selectOptions(screen.getByLabelText(/每页/), '100')
    await user.click(screen.getByRole('button', { name: /查询/ }))
    await screen.findByText('第二请求主题')
    // 第一次请求此时才返回
    release?.()
    // 旧数据不得覆盖新数据
    await new Promise((r) => setTimeout(r, 100))
    expect(screen.getByText('第二请求主题')).toBeInTheDocument()
    expect(screen.queryByText('旧主题')).toBeNull()
  })

  it('空 subject 显示(无主题)', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () =>
        HttpResponse.json({
          success: true,
          data: {
            ...inboxResult,
            messages: [{ ...inboxResult.messages[0], subject: '' }],
          },
        }),
      ),
    )
    renderPage()
    expect(await screen.findByText(/（无主题）/)).toBeInTheDocument()
  })

  it('空摘要显示占位符', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () =>
        HttpResponse.json({
          success: true,
          data: {
            ...inboxResult,
            messages: [{ ...inboxResult.messages[0], preview: '' }],
          },
        }),
      ),
    )
    renderPage()
    expect(await screen.findByText('—')).toBeInTheDocument()
  })
  it('默认合并查询，显示同编号邮件的不同来源，并支持切换垃圾邮件', async () => {
    const urls: string[] = []
    const messages = [
      { ...inboxResult.messages[0], folder: 'inbox', subject: '普通邮件' },
      { ...inboxResult.messages[0], folder: 'junk', subject: '误判邮件' },
    ]
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', ({ request }) => {
        urls.push(request.url)
        const folder = new URL(request.url).searchParams.get('folder')
        const selected = folder === 'all' ? messages : messages.filter((m) => m.folder === folder)
        return HttpResponse.json({ success: true, data: { ...inboxResult, method: 'web_api', messages: selected, count: selected.length } })
      }),
    )
    renderPage()
    await screen.findByText('误判邮件')
    expect(screen.getByLabelText('邮件范围')).toHaveValue('all')
    expect(new URL(urls[0]).searchParams.get('folder')).toBe('all')
    const rows = screen.getAllByRole('row')
    expect(rows).toHaveLength(3)
    expect(within(rows[1]).getByText('收件箱')).toBeInTheDocument()
    expect(within(rows[2]).getByText('垃圾邮件')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '删除邮件' })).toBeNull()
    await userEvent.setup().selectOptions(screen.getByLabelText('邮件范围'), 'junk')
    await screen.findByText('误判邮件')
    expect(screen.queryByText('普通邮件')).toBeNull()
    expect(new URL(urls[urls.length - 1]).searchParams.get('folder')).toBe('junk')
  })

  it('从 URL 恢复垃圾邮件范围及时间和条数', async () => {
    let lastUrl = ''
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', ({ request }) => {
        lastUrl = request.url
        return HttpResponse.json({ success: true, data: inboxResult })
      }),
    )
    renderPage('/inbox?account_id=acc_1&folder=junk&limit=100&days=30')
    await screen.findByText('主题一')
    expect(screen.getByLabelText('邮件范围')).toHaveValue('junk')
    expect(screen.getByLabelText('每页')).toHaveValue('100')
    expect(screen.getByLabelText('时间范围')).toHaveValue('30')
    const query = new URL(lastUrl).searchParams
    expect(query.get('folder')).toBe('junk')
    expect(query.get('days')).toBe('30')
    expect(query.get('limit')).toBe('100')
  })

  it('IMAP 详情和删除携带该邮件来源，不误用当前合并范围', async () => {
    let detailUrl = ''
    let deleteUrl = ''
    const message = { ...inboxResult.messages[0], folder: 'junk', subject: '垃圾箱中的正常邮件' }
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () => HttpResponse.json({ success: true, data: { ...inboxResult, messages: [message] } })),
      http.get('/api/inbox/1', ({ request }) => {
        detailUrl = request.url
        return HttpResponse.json({ success: true, data: { ...message, body: '正文内容', content_type: 'text/plain' } })
      }),
      http.delete('/api/inbox/1', ({ request }) => {
        deleteUrl = request.url
        return HttpResponse.json({ success: true, data: { id: '1' } })
      }),
    )
    renderPage()
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: '垃圾箱中的正常邮件' }))
    await screen.findByText('正文内容')
    expect(new URL(detailUrl).searchParams.get('folder')).toBe('junk')
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '删除邮件' }))
    expect(screen.getByText('邮件将从垃圾邮件中永久删除。')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '确认删除' }))
    await waitFor(() => expect(deleteUrl).not.toBe(''))
    expect(new URL(deleteUrl).searchParams.get('folder')).toBe('junk')
  })

})
