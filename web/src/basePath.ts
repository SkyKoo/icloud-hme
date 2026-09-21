/** Go 输出的部署路径;Vite 开发服务器默认使用根路径。 */
export function basePath(): string {
  return document.querySelector<HTMLMetaElement>('meta[name="hme-base-path"]')?.content ?? '/'
}
