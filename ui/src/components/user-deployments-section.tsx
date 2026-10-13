import { ExternalLink, Loader2, Plus, RefreshCw, Square, Trash2, RotateCcw } from "lucide-react"
import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { cn } from "@/lib/utils"
import { toast } from "sonner"

export type Deployment = {
  id: string
  github_installation_id: number
  repository_full_name: string
  ref?: string
  source?: "branch" | "tag"
  commit_sha: string
  config_path: string
  status: string
  template_id?: string
  sandbox_id?: string
  endpoint?: string
  endpoint_access?: string
  failure_stage?: string
  failure_reason?: string
  expires_at: string
  created_at: string
  updated_at: string
}

type DeploymentEvent = {
  id: number
  event_type: string
  stage?: string
  stream?: string
  message: string
  created_at: string
}

type Request = (url: string, options?: RequestInit) => Promise<unknown>

type EventPage = { events?: DeploymentEvent[]; has_more?: boolean }

const activeStatuses = new Set(["queued", "planning", "creating", "running", "stopping"])

export function UserDeploymentsSection({
  currentLogin,
  request,
  deploymentID,
  onOpenDeployment,
}: {
  currentLogin?: string
  request: Request
  deploymentID?: string
  onOpenDeployment: (id: string | null) => void
}) {
  const { t, i18n } = useTranslation()
  const [deployments, setDeployments] = useState<Deployment[]>([])
  const selectedID = deploymentID ?? null
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [action, setAction] = useState<string | null>(null)
  const generation = useRef(0)
  const selectedDeployment = deployments.find((item) => item.id === selectedID) ?? null
  const deploymentEvents = useDeploymentEvents(request, selectedID, selectedDeployment?.status, currentLogin)

  const formatTime = useCallback((value?: string) => {
    if (!value) return "—"
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return value
    return new Intl.DateTimeFormat(i18n.resolvedLanguage === "zh" ? "zh-CN" : "en", {
      dateStyle: "medium",
      timeStyle: "short",
    }).format(parsed)
  }, [i18n.resolvedLanguage])

  const loadDeployments = useCallback(async () => {
    const requestGeneration = ++generation.current
    setLoading(true)
    setError(null)
    try {
      const data = await request(deploymentID ? `/user/deployments/${encodeURIComponent(deploymentID)}` : "/user/deployments") as { items?: Deployment[] } & Deployment
      if (requestGeneration !== generation.current) return
      const next = deploymentID ? [data] : Array.isArray(data.items) ? data.items : []
      setDeployments(next)
    } catch (cause) {
      if (requestGeneration !== generation.current) return
      setError(cause instanceof Error ? cause.message : t("user.deploymentLoadFailed"))
    } finally {
      if (requestGeneration === generation.current) setLoading(false)
    }
  }, [request, t, deploymentID])

  useEffect(() => {
    void loadDeployments()
    return () => {
      generation.current += 1
    }
  }, [loadDeployments, currentLogin])

  useEffect(() => {
    if (!deployments.some((item) => activeStatuses.has(item.status))) return
    const timer = window.setInterval(() => { void loadDeployments() }, 3000)
    return () => {
      window.clearInterval(timer)
    }
  }, [loadDeployments, deployments])

  async function runAction(kind: "stop" | "delete" | "retry") {
    if (!selectedDeployment || action) return
    setAction(kind)
    try {
      const path = `/user/deployments/${encodeURIComponent(selectedDeployment.id)}`
      const result = await request(kind === "delete" ? path : `${path}/${kind}`, { method: kind === "delete" ? "DELETE" : "POST" }) as Deployment
      setDeployments((current) => kind === "delete" ? current.filter((item) => item.id !== selectedDeployment.id) : current.map((item) => item.id === result.id ? result : item))
      if (kind === "delete" && deploymentID) onOpenDeployment(null)
      toast.success(t(`user.deployment${kind[0].toUpperCase()}${kind.slice(1)}d` as "user.deploymentStopped" | "user.deploymentDeleted" | "user.deploymentRetried"))
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : t("user.deploymentActionFailed"))
    } finally {
      setAction(null)
    }
  }

  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      <section className="border-b bg-muted/35 px-4 py-4 lg:px-6">
        <div className="mx-auto flex w-full max-w-7xl flex-wrap items-start justify-between gap-4">
          <div>
            <h1 className="mt-1 text-2xl font-semibold tracking-tight">{deploymentID ? t("user.deploymentDetailTitle") : t("user.deployments")}</h1>
            <p className="mt-1 max-w-2xl text-sm text-muted-foreground">{t("user.deploymentListDescription")}</p>
          </div>
          <div className="flex shrink-0 flex-wrap gap-2">
            {!deploymentID ? <Button asChild><a href="/deployments/new"><Plus className="h-4 w-4" />{t("user.createDeployment")}</a></Button> : null}
            <Button type="button" variant="outline" onClick={() => void loadDeployments()} disabled={loading}>
              <RefreshCw className={cn("h-4 w-4", loading && "animate-spin")} />
              {t("common.refresh")}
            </Button>
          </div>
        </div>
      </section>

      <section className="mx-auto w-full max-w-7xl space-y-4 p-4 lg:p-6">
        {deploymentID ? <Button type="button" variant="outline" className="w-fit" onClick={() => onOpenDeployment(null)}>{t("user.deploymentBack")}</Button> : null}
        <div className="min-w-0 space-y-4">
          {error ? <div className="rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive">{error}</div> : null}
          {loading && deployments.length === 0 ? <div className="flex items-center gap-2 rounded-lg border p-6 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />{t("user.loadingDeployments")}</div> : deployments.length === 0 ? <div className="rounded-lg border border-dashed p-8 text-center"><h2 className="font-semibold">{t("user.noDeployments")}</h2><p className="mt-1 text-sm text-muted-foreground">{t("user.noDeploymentsDescription")}</p></div> : (
            <div className="space-y-4">
              {!deploymentID ? <div className="space-y-2" data-testid="deployment-list">
                {deployments.map((deployment) => <a key={deployment.id} href={`/deployments/${encodeURIComponent(deployment.id)}`} className="block w-full rounded-lg border p-4 transition-colors hover:bg-accent">
                  <div className="flex items-start justify-between gap-2"><span className="truncate text-sm font-semibold">{deployment.repository_full_name}</span><DeploymentStatus status={deployment.status} /></div>
                  <div className="mt-2 text-xs text-muted-foreground">{deployment.ref || deployment.commit_sha.slice(0, 12)}</div>
                  <div className="mt-1 text-xs text-muted-foreground">{formatTime(deployment.created_at)}</div>
                </a>)}
              </div> : null}
              {selectedDeployment ? <DeploymentDetail deployment={selectedDeployment} eventState={deploymentEvents} formatTime={formatTime} action={action} onAction={runAction} /> : null}
            </div>
          )}
        </div>
      </section>
    </div>
  )
}

function DeploymentDetail({ deployment, eventState, formatTime, action, onAction }: { deployment: Deployment; eventState: ReturnType<typeof useDeploymentEvents>; formatTime: (value?: string) => string; action: string | null; onAction: (kind: "stop" | "delete" | "retry") => void }) {
  const { t } = useTranslation()
  const { events, loading, error, hasOlder, refresh, loadOlder } = eventState
  const canStop = activeStatuses.has(deployment.status)
  const canRetry = ["failed", "completed", "expired"].includes(deployment.status)
  return <Card>
    <CardHeader className="border-b">
      <div className="flex flex-wrap items-start justify-between gap-3"><div className="min-w-0"><CardTitle className="truncate text-lg">{deployment.repository_full_name}</CardTitle><CardDescription className="mt-1">{deployment.config_path} · {deployment.ref || deployment.commit_sha.slice(0, 12)}</CardDescription></div><DeploymentStatus status={deployment.status} /></div>
      <div className="flex flex-wrap gap-2 pt-2"><Button type="button" size="sm" variant="outline" disabled={!canStop || Boolean(action)} onClick={() => onAction("stop")}><Square className="h-3.5 w-3.5" />{t("user.stopDeployment")}</Button><Button type="button" size="sm" variant="outline" disabled={!canRetry || Boolean(action)} onClick={() => onAction("retry")}><RotateCcw className="h-3.5 w-3.5" />{t("user.retryDeployment")}</Button><Button type="button" size="sm" variant="ghost" disabled={Boolean(action)} onClick={() => onAction("delete")}><Trash2 className="h-3.5 w-3.5" />{t("common.delete")}</Button></div>
    </CardHeader>
    <CardContent className="space-y-5 pt-5">
      <dl className="grid gap-4 sm:grid-cols-2"><Info label={t("user.deploymentCommit")} value={deployment.commit_sha} mono /><Info label={t("user.deploymentTemplate")} value={deployment.template_id || "—"} mono /><Info label={t("user.deploymentExpires")} value={formatTime(deployment.expires_at)} /><Info label={t("user.deploymentCreatedAt")} value={formatTime(deployment.created_at)} /><Info label={t("user.deploymentSandbox")} value={deployment.sandbox_id || "—"} mono /></dl>
      {deployment.endpoint ? <div className="space-y-2"><a className="flex items-center gap-2 rounded-md border bg-muted/30 p-3 text-sm font-medium text-primary hover:underline" href={deployment.endpoint} target="_blank" rel="noopener noreferrer">{deployment.endpoint}<ExternalLink className="h-3.5 w-3.5" /></a>{deployment.endpoint_access === "public" ? <p className="text-xs text-muted-foreground">{t("user.deploymentEndpointPublic")}</p> : null}</div> : null}
      {deployment.failure_reason ? <div className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm"><div className="font-medium text-destructive">{deployment.failure_stage || t("user.deploymentFailure")}</div><div className="mt-1 text-muted-foreground">{deployment.failure_reason}</div></div> : null}
      <div>
        <div className="mb-2 flex items-center justify-between gap-2"><h3 className="text-sm font-semibold">{t("user.deploymentEvents")}</h3><Button type="button" variant="ghost" size="sm" disabled={loading} onClick={() => void refresh()}>{loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}{t("common.refresh")}</Button></div>
        {error ? <p role="alert" className="mb-2 text-sm text-destructive">{t("user.deploymentEventsFailed")}: {error}</p> : null}
        {hasOlder ? <Button type="button" variant="outline" size="sm" className="mb-2" disabled={loading} onClick={() => void loadOlder()}>{t("user.deploymentEventsOlder")}</Button> : null}
        {events.length === 0 ? <p className="text-sm text-muted-foreground">{t("user.deploymentNoEvents")}</p> : <div data-testid="deployment-logs" className="max-h-[32rem] overflow-auto rounded-md border bg-muted/20 p-3 font-mono text-xs leading-relaxed">{events.map((event) => <div key={event.id} className="border-b border-border/40 py-1.5 last:border-0"><div className="flex flex-wrap gap-2 text-muted-foreground"><span>{event.stage || event.event_type}</span><span>{formatTime(event.created_at)}</span>{event.stream ? <span>{event.stream}</span> : null}</div><pre className="whitespace-pre-wrap break-words">{event.message}</pre></div>)}</div>}
      </div>
    </CardContent>
  </Card>
}

function Info({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div><dt className="text-xs text-muted-foreground">{label}</dt><dd className={cn("mt-1 break-all text-sm", mono && "font-mono")}>{value}</dd></div>
}

function DeploymentStatus({ status }: { status: string }) {
  const { t } = useTranslation()
  const label: Record<string, string> = { queued: t("common.statusQueued"), planning: t("user.deploymentPlanning"), creating: t("common.statusCreating"), running: t("common.statusRunning"), stopping: t("common.statusStopping"), completed: t("common.statusCompleted"), expired: t("user.deploymentExpired"), failed: t("common.statusFailed") }
  return <Badge variant={status === "failed" ? "destructive" : status === "running" ? "default" : "secondary"}>{label[status] || status}</Badge>
}

function useDeploymentEvents(request: Request, id: string | null, status?: string, login?: string) {
  const [events, setEvents] = useState<DeploymentEvent[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [hasOlder, setHasOlder] = useState(false)
  const generation = useRef(0)
  const cursor = useRef({ newest: 0, oldest: 0, busy: false })

  useEffect(() => {
    generation.current += 1
    cursor.current = { newest: 0, oldest: 0, busy: false }
    setEvents([])
    setError(null)
    setLoading(false)
    setHasOlder(false)
    return () => { generation.current += 1 }
  }, [id, request, login])

  const load = useCallback(async (older = false) => {
    if (!id || cursor.current.busy) return
    const epoch = generation.current
    cursor.current.busy = true
    setLoading(true)
    setError(null)
    try {
      let next = older ? cursor.current.oldest : cursor.current.newest
      const initial = next === 0
      do {
        const query = next > 0 ? `?${older ? "before_id" : "after_id"}=${next}` : ""
        const page = await request(`/user/deployments/${encodeURIComponent(id)}/events${query}`) as EventPage
        if (epoch !== generation.current) return
        if (!Array.isArray(page.events)) throw new Error("Invalid deployment events response")
        const incoming = page.events
        setEvents((current) => {
          const merged = new Map(current.map((event) => [event.id, event]))
          incoming.forEach((event) => merged.set(event.id, event))
          return [...merged.values()].sort((a, b) => a.id - b.id)
        })
        if (incoming.length > 0) {
          cursor.current.newest = Math.max(cursor.current.newest, incoming[incoming.length - 1].id)
          if (initial || older) cursor.current.oldest = incoming[0].id
        }
        if (initial || older) setHasOlder(Boolean(page.has_more))
        if (initial || older || !page.has_more || incoming.length === 0) break
        next = incoming[incoming.length - 1].id
      } while (epoch === generation.current)
    } catch (cause) {
      if (epoch === generation.current) setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      if (epoch === generation.current) {
        cursor.current.busy = false
        setLoading(false)
      }
    }
  }, [id, request])

  const refresh = useCallback(() => load(false), [load])
  const loadOlder = useCallback(() => load(true), [load])
  useEffect(() => {
    void refresh()
    if (!status || !activeStatuses.has(status)) return
    const timer = window.setInterval(() => { void refresh() }, 3000)
    return () => window.clearInterval(timer)
  }, [refresh, status, login])

  return { events, loading, error, hasOlder, refresh, loadOlder }
}
