import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import MailboxDialog from './MailboxDialog'
import type { MailboxSummary } from '../api/types'

const current: MailboxSummary = {
  provider: 'qq',
  email: 'saved@example.com',
  imap_host: 'imap.qq.com',
  imap_port: 993,
}

describe('MailboxDialog', () => {
  it('关闭后重新打开时恢复保存值并清空未提交的授权码', () => {
    const props = { accountId: 'account-a', current, onClose: vi.fn(), onSaved: vi.fn() }
    const { rerender } = render(<MailboxDialog {...props} open />)

    fireEvent.change(screen.getByLabelText('收件邮箱'), { target: { value: 'draft@example.com' } })
    fireEvent.change(screen.getByLabelText('邮箱授权码'), { target: { value: 'test-only-code' } })
    rerender(<MailboxDialog {...props} open={false} />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    rerender(<MailboxDialog {...props} open />)
    expect(screen.getByLabelText('收件邮箱')).toHaveValue('saved@example.com')
    expect(screen.getByLabelText('邮箱授权码')).toHaveValue('')
  })

  it('切换账户或收到更新后的配置时重置表单', () => {
    const props = { current, onClose: vi.fn(), onSaved: vi.fn(), open: true }
    const { rerender } = render(<MailboxDialog {...props} accountId="account-a" />)
    fireEvent.change(screen.getByLabelText('邮箱授权码'), { target: { value: 'test-only-code' } })

    rerender(<MailboxDialog {...props} accountId="account-b" />)
    expect(screen.getByLabelText('邮箱授权码')).toHaveValue('')

    const updated = { ...current, provider: 'gmail', email: 'updated@example.com', imap_host: 'imap.gmail.com' }
    rerender(<MailboxDialog {...props} accountId="account-b" current={updated} />)
    expect(screen.getByLabelText('邮箱服务商')).toHaveValue('gmail')
    expect(screen.getByLabelText('收件邮箱')).toHaveValue('updated@example.com')
    expect(screen.getByLabelText('IMAP 服务器')).toHaveValue('imap.gmail.com')
  })
})
