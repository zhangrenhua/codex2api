import { useTranslation } from 'react-i18next'
import PageHeader from '../components/PageHeader'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'

export default function Guide() {
  const { t } = useTranslation()

  return (
    <>
      <PageHeader
        title={t('guide.title')}
        description={t('guide.description')}
      />

      {/* API 端点总览 */}
      <Card className="mb-4">
        <CardContent className="p-6">
          <h3 className="text-base font-semibold text-foreground mb-2">{t('guide.endpoints')}</h3>
          <p className="text-sm text-muted-foreground mb-4">{t('guide.endpointsDesc')}</p>

          <div className="space-y-4">
            <div className="p-4 rounded-xl border border-border bg-white/40 dark:bg-white/5">
              <div className="flex items-center gap-2 mb-2">
                <Badge variant="default" className="text-[13px]">POST</Badge>
                <code className="font-mono text-sm font-semibold">/v1/responses</code>
              </div>
              <p className="text-sm text-muted-foreground">{t('guide.responsesDesc')}</p>
            </div>

            <div className="p-4 rounded-xl border border-border bg-white/40 dark:bg-white/5">
              <div className="flex items-center gap-2 mb-2">
                <Badge variant="default" className="text-[13px]">POST</Badge>
                <code className="font-mono text-sm font-semibold">/v1/chat/completions</code>
              </div>
              <p className="text-sm text-muted-foreground">{t('guide.chatDesc')}</p>
            </div>

            <div className="p-4 rounded-xl border border-border bg-white/40 dark:bg-white/5">
              <div className="flex items-center gap-2 mb-2">
                <Badge variant="default" className="text-[13px]">POST</Badge>
                <code className="font-mono text-sm font-semibold">/v1/messages</code>
              </div>
              <p className="text-sm text-muted-foreground">{t('guide.messagesDesc')}</p>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* Claude Code 配置 */}
      <Card className="mb-4">
        <CardContent className="p-6">
          <h3 className="text-base font-semibold text-foreground mb-2">{t('guide.claudeCodeTitle')}</h3>
          <p className="text-sm text-muted-foreground mb-4">{t('guide.claudeCodeDesc')}</p>

          <pre className="p-4 rounded-xl bg-zinc-900 text-zinc-100 text-sm font-mono overflow-x-auto">
{`export ANTHROPIC_BASE_URL=http://your-server:8080
export ANTHROPIC_AUTH_TOKEN=YOUR_API_KEY`}
          </pre>
          <p className="text-xs text-muted-foreground mt-3">{t('guide.claudeCodeNote')}</p>
        </CardContent>
      </Card>

      {/* 认证方式 */}
      <Card className="mb-4">
        <CardContent className="p-6">
          <h3 className="text-base font-semibold text-foreground mb-2">{t('guide.authTitle')}</h3>
          <p className="text-sm text-muted-foreground mb-3">{t('guide.authDesc')}</p>
          <ul className="space-y-2 text-sm">
            <li className="flex items-start gap-2">
              <span className="text-primary font-bold mt-0.5">1.</span>
              <code className="font-mono text-[13px] bg-muted px-2 py-0.5 rounded">{t('guide.authBearer')}</code>
            </li>
            <li className="flex items-start gap-2">
              <span className="text-primary font-bold mt-0.5">2.</span>
              <code className="font-mono text-[13px] bg-muted px-2 py-0.5 rounded">{t('guide.authXApiKey')}</code>
            </li>
            <li className="flex items-start gap-2">
              <span className="text-primary font-bold mt-0.5">3.</span>
              <code className="font-mono text-[13px] bg-muted px-2 py-0.5 rounded">{t('guide.authAnthropicToken')}</code>
            </li>
          </ul>
        </CardContent>
      </Card>

      {/* 模型映射说明 */}
      <Card className="mb-4">
        <CardContent className="p-6">
          <h3 className="text-base font-semibold text-foreground mb-2">{t('guide.modelMappingTitle')}</h3>
          <p className="text-sm text-muted-foreground">{t('guide.modelMappingDesc')}</p>
        </CardContent>
      </Card>

      {/* 请求示例 */}
      <Card>
        <CardContent className="p-6">
          <h3 className="text-base font-semibold text-foreground mb-4">{t('guide.exampleTitle')}</h3>

          <div className="space-y-4">
            <div>
              <h4 className="text-sm font-semibold text-muted-foreground mb-2">{t('guide.responsesExample')}</h4>
              <pre className="p-4 rounded-xl bg-zinc-900 text-zinc-100 text-[13px] font-mono overflow-x-auto">
{`curl -X POST http://your-server:8080/v1/responses \\
  -H "Authorization: Bearer YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-5.4",
    "input": [{"role": "user", "content": [{"type": "input_text", "text": "Hello"}]}],
    "stream": true
  }'`}
              </pre>
            </div>

            <div>
              <h4 className="text-sm font-semibold text-muted-foreground mb-2">{t('guide.chatExample')}</h4>
              <pre className="p-4 rounded-xl bg-zinc-900 text-zinc-100 text-[13px] font-mono overflow-x-auto">
{`curl -X POST http://your-server:8080/v1/chat/completions \\
  -H "Authorization: Bearer YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-5.4",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true
  }'`}
              </pre>
            </div>

            <div>
              <h4 className="text-sm font-semibold text-muted-foreground mb-2">{t('guide.messagesExample')}</h4>
              <pre className="p-4 rounded-xl bg-zinc-900 text-zinc-100 text-[13px] font-mono overflow-x-auto">
{`curl -X POST http://your-server:8080/v1/messages \\
  -H "x-api-key: YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -H "anthropic-version: 2023-06-01" \\
  -d '{
    "model": "claude-sonnet-4-5-20250514",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello"}]
  }'`}
              </pre>
            </div>
          </div>
        </CardContent>
      </Card>
    </>
  )
}
