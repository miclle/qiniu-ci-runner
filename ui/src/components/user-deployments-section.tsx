import { deploymentContext, deploymentListAPIPath, deploymentPath, githubContextFromPath, githubContextPath } from "@/github-context-routes"
import { ArrowLeft, CalendarDays, GitBranch, GitCommitHorizontal, ExternalLink, Loader2, Plus, RefreshCw, Square, Trash2, RotateCcw } from "lucide-react"
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { cn } from "@/lib/utils"
import { toast } from "sonner"

const DeploymentTerminal = lazy(() => import("@/components/deployment-terminal"))

export type Deployment = {
  id: string
  github_installation_id: number
  repository_full_name: string
  ref?: string
  source?: "branch" | "tag" | "pull_request"
  pull_request_number?: number
  source_repository_full_name?: string
  source_ref?: string
  commit_sha: string
  config_path: string
  status: string
  template_id?: string
  template_name?: string
  template_cpu?: number
  template_memory_mb?: number
  template_disk_mb?: number
  sandbox_id?: string
  sandbox_region?: string
  endpoint?: string
  endpoint_access?: string
  failure_stage?: string
  failure_reason?: string
  expires_at: string
  created_at: string
  updated_at: string
  last_successful_revision_number?: number
  revision_number?: number
  revisions?: DeploymentRevision[]
  revisions_has_more?: boolean
  selected_revision?: DeploymentRevision
}

export type DeploymentRevision = {
  id: string
  number: number
  status: string
  ref?: string
  commit_sha: string
  config_path: string
  template_id?: string
  template_name?: string
  template_cpu?: number
  template_memory_mb?: number
  template_disk_mb?: number
  sandbox_id?: string
  sandbox_region?: string
  endpoint?: string
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

type DeploymentResources = Pick<Deployment, "template_id" | "template_name" | "template_cpu" | "template_memory_mb" | "template_disk_mb" | "sandbox_id" | "sandbox_region" | "expires_at"> & { remote_error?: string }

const activeStatuses = new Set(["queued", "planning", "creating", "running", "stopping"])

export function UserDeploymentsSection({
  currentLogin,
  request,
  deploymentID,
  locationPath = window.location.pathname,
  locationSearch = window.location.search,
  onOpenDeployment,
}: {
  currentLogin?: string
  request: Request
  deploymentID?: string
  locationPath?: string
  locationSearch?: string
  onOpenDeployment: (id: string | null) => void
}) {
  const { t, i18n } = useTranslation()
  const [deployments, setDeployments] = useState<Deployment[]>([])
  const selectedID = deploymentID ?? null
  const context = githubContextFromPath(locationPath, locationSearch)
  const listPath = deploymentListAPIPath(context)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [action, setAction] = useState<string | null>(null)
  const generation = useRef(0)
  const selectedDeployment = deployments.find((item) => item.id === selectedID) ?? null
  const [revisionSelection, setRevisionSelection] = useState<{ id: string; number?: number } | null>(null)
  const requestedRevision = revisionSelection?.id === selectedID ? revisionSelection.number : revisionNumberFromURL()
  const selectedRevisionNumber = requestedRevision ?? selectedDeployment?.revision_number
  const selectRevision = useCallback((number: number) => {
    if (!selectedID) return
    setRevisionSelection({ id: selectedID, number })
    const url = new URL(window.location.href)
    url.searchParams.set("revision", String(number))
    window.history.pushState(null, "", url)
  }, [selectedID])
  useEffect(() => {
    const onHistory = () => setRevisionSelection({ id: selectedID ?? "", number: revisionNumberFromURL() })
    window.addEventListener("popstate", onHistory)
    return () => window.removeEventListener("popstate", onHistory)
  }, [selectedID])
  const selectedRevision = selectedDeployment?.revisions?.find((item) => item.number === selectedRevisionNumber) ?? (selectedDeployment?.selected_revision && selectedDeployment.selected_revision.number === selectedRevisionNumber ? selectedDeployment.selected_revision : null)
  useEffect(() => {
    if (!selectedDeployment || (requestedRevision && !selectedRevision)) return
    const canonical = deploymentPath(selectedDeployment, selectedRevision, requestedRevision)
    if (window.location.pathname + window.location.search === canonical) return
    window.history.replaceState(null, "", canonical)
    window.dispatchEvent(new window.PopStateEvent("popstate"))
  }, [selectedDeployment, selectedRevision, requestedRevision])
  const backToDeployments = () => {
    const source = selectedDeployment ? deploymentContext(selectedDeployment, selectedRevision) : context
    if (!source) { onOpenDeployment(null); return }
    window.history.pushState(null, "", githubContextPath(source, "deployments"))
    window.dispatchEvent(new window.PopStateEvent("popstate"))
  }
  const deploymentEvents = useDeploymentEvents(request, selectedDeployment ? selectedID : null, selectedRevisionNumber, selectedRevision?.status ?? selectedDeployment?.status, currentLogin)
  const selectedDeploymentID = selectedDeployment?.id
  const [deploymentResources, setDeploymentResources] = useState<DeploymentResources | null>(null)
  const [deploymentResourcesLoading, setDeploymentResourcesLoading] = useState(false)

  useEffect(() => {
    if (!selectedDeploymentID) {
      setDeploymentResources(null)
      setDeploymentResourcesLoading(false)
      return
    }
    let cancelled = false
    setDeploymentResources(null)
    setDeploymentResourcesLoading(true)
    const revisionQuery = selectedRevisionNumber ? `?revision=${selectedRevisionNumber}` : ""
    void request(`/user/deployments/${encodeURIComponent(selectedDeploymentID)}/resources${revisionQuery}`)
      .then((value) => {
        if (!cancelled) setDeploymentResources(value as DeploymentResources)
      })
      .catch(() => {
        // Provider reads are best effort. The detail view still renders the
        // immutable local snapshot when the Sandbox API is unavailable.
      })
      .finally(() => {
        if (!cancelled) setDeploymentResourcesLoading(false)
      })
    return () => { cancelled = true }
  }, [request, selectedDeploymentID, selectedRevisionNumber])

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
      const data = await request(deploymentID ? `/user/deployments/${encodeURIComponent(deploymentID)}${requestedRevision ? `?revision=${requestedRevision}` : ""}` : listPath) as { items?: Deployment[] } & Deployment
      if (requestGeneration !== generation.current) return
      const next = deploymentID ? [data] : Array.isArray(data.items) ? data.items : []
      setDeployments((current) => next.map((item) => {
        const previous = current.find((old) => old.id === item.id)
        if (!previous?.revisions?.length || !item.revisions?.length) return item
        const merged = new Map(previous.revisions.map((revision) => [revision.number, revision]))
        item.revisions.forEach((revision) => merged.set(revision.number, revision))
        return { ...item, revisions: [...merged.values()].sort((a, b) => b.number - a.number), revisions_has_more: previous.revisions.length > item.revisions.length ? previous.revisions_has_more : item.revisions_has_more }
      }))
    } catch (cause) {
      if (requestGeneration !== generation.current) return
      setError(cause instanceof Error ? cause.message : t("user.deploymentLoadFailed"))
    } finally {
      if (requestGeneration === generation.current) setLoading(false)
    }
  }, [request, t, deploymentID, requestedRevision, listPath])

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

  const loadOlderRevisions = async () => {
    if (!selectedDeployment || !selectedDeployment.revisions_has_more) return
    const id = selectedDeployment.id
    const epoch = generation.current
    const last = selectedDeployment.revisions?.[selectedDeployment.revisions.length - 1]?.number
    if (!last) return
    try {
      const page = await request(`/user/deployments/${encodeURIComponent(id)}/revisions?before_number=${last}`) as { revisions: DeploymentRevision[]; has_more: boolean }
      if (epoch !== generation.current) return
      setDeployments((current) => current.map((item) => item.id === id ? { ...item, revisions: [...(item.revisions ?? []), ...page.revisions], revisions_has_more: page.has_more } : item))
    } catch (cause) {
      if (epoch === generation.current) setError(cause instanceof Error ? cause.message : t("user.deploymentLoadFailed"))
    }
  }

  async function runAction(kind: "stop" | "delete" | "retry", retryOptions?: { mode: "latest" | "revision"; revision_number?: number }) {
    if (!selectedDeployment || action) return
    setAction(kind)
    try {
      const path = `/user/deployments/${encodeURIComponent(selectedDeployment.id)}`
      const result = await request(kind === "delete" ? path : `${path}/${kind}`, { method: kind === "delete" ? "DELETE" : "POST", ...(kind === "retry" ? { headers: { "Content-Type": "application/json" }, body: JSON.stringify(retryOptions ?? { mode: "revision" }) } : {}) }) as Deployment
      if (kind === "retry") { setRevisionSelection({ id: result.id, number: result.revision_number }); const url = new URL(window.location.href); url.searchParams.delete("revision"); window.history.replaceState(null, "", url) }
      setDeployments((current) => kind === "delete" ? current.filter((item) => item.id !== selectedDeployment.id) : current.map((item) => item.id === result.id ? { ...result, revisions: item.revisions, revisions_has_more: item.revisions_has_more } : item))
      if (kind !== "delete") void loadDeployments()
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
        <div className="flex w-full flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            {deploymentID ? <Button type="button" variant="outline" size="icon" aria-label={t("user.deploymentBack")} onClick={backToDeployments}><ArrowLeft /></Button> : null}
            <div>
              <h1 className="text-xl font-semibold">{deploymentID ? t("user.deploymentDetailTitle") : t("user.deployments")}</h1>
              <p className="mt-1 max-w-2xl text-sm text-muted-foreground">{t("user.deploymentListDescription")}</p>
            </div>
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

      <section className="w-full min-w-0 space-y-4 p-4 lg:p-6">
        <div className="min-w-0 space-y-4">
          {error ? <div className="rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm text-destructive">{error}</div> : null}
          {loading && deployments.length === 0 ? <div className="flex items-center gap-2 rounded-lg border p-6 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />{t("user.loadingDeployments")}</div> : deployments.length === 0 ? <div className="rounded-lg border border-dashed p-8 text-center"><h2 className="font-semibold">{t("user.noDeployments")}</h2><p className="mt-1 text-sm text-muted-foreground">{t("user.noDeploymentsDescription")}</p></div> : (
            <div className="space-y-4">
              {!deploymentID ? <div className="divide-y border-y" data-testid="deployment-list">
                {deployments.map((deployment) => <a key={deployment.id} href={deploymentPath(deployment, deployment.selected_revision)} className="group relative flex w-full items-start gap-3 bg-background/60 px-4 py-4 transition-colors hover:bg-accent/70 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring">
                  <span aria-hidden="true" className={cn("absolute inset-y-0 left-0 w-1", deployment.status === "failed" ? "bg-destructive" : deployment.status === "running" ? "bg-emerald-500" : activeStatuses.has(deployment.status) ? "bg-yellow-500" : "bg-muted-foreground/40")} />
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center justify-between gap-2"><span className="min-w-0 break-all text-sm font-semibold">{deployment.repository_full_name}</span><DeploymentStatus status={deployment.status} /></div>
                    <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs text-muted-foreground">
                      <span className="inline-flex min-w-0 items-center gap-1"><GitBranch className="h-3.5 w-3.5 shrink-0" /><span className="break-all">{displayRevisionRef(deployment.ref) || "—"}</span></span>
                      <span className="inline-flex items-center gap-1"><GitCommitHorizontal className="h-3.5 w-3.5" /><code>{deployment.commit_sha.slice(0, 8)}</code></span>
                      <span className="inline-flex items-center gap-1"><CalendarDays className="h-3.5 w-3.5" />{formatTime(deployment.created_at)}</span>
                    </div>
                  </div>
                </a>)}
              </div> : null}
              {selectedDeployment ? <DeploymentDetail request={request} selectedRevisionNumber={selectedRevisionNumber} deployment={selectedDeployment} selectedRevision={selectedRevision} resources={deploymentResources} resourcesLoading={deploymentResourcesLoading} onSelectRevision={selectRevision} onLoadOlderRevisions={loadOlderRevisions} eventState={deploymentEvents} formatTime={formatTime} action={action} onAction={runAction} /> : null}
            </div>
          )}
        </div>
      </section>
    </div>
  )
}

function DeploymentDetail({ request, selectedRevisionNumber, deployment, selectedRevision, resources, resourcesLoading, onSelectRevision, onLoadOlderRevisions, eventState, formatTime, action, onAction }: {
  request: Request
  selectedRevisionNumber?: number
  deployment: Deployment
  selectedRevision: DeploymentRevision | null
  resources: DeploymentResources | null
  resourcesLoading: boolean
  onSelectRevision: (number: number) => void
  onLoadOlderRevisions: () => void
  eventState: ReturnType<typeof useDeploymentEvents>
  formatTime: (value?: string) => string
  action: string | null
  onAction: (kind: "stop" | "delete" | "retry", retryOptions?: { mode: "latest" | "revision"; revision_number?: number }) => void
}) {
  const { t, i18n } = useTranslation()
  const { events, loading, error, hasOlder, refresh, loadOlder } = eventState
  const canStop = activeStatuses.has(deployment.status)
  const canRetry = ["failed", "completed", "expired"].includes(deployment.status)
  const terminalAvailable = deployment.status === "running" && Boolean(deployment.sandbox_id) && selectedRevisionNumber === deployment.revision_number
  const revisions = deployment.revisions ?? []
  const [redeployOpen, setRedeployOpen] = useState(false)
  const [redeployMode, setRedeployMode] = useState<"latest" | "revision">(deployment.source === "branch" || deployment.source === "pull_request" ? "latest" : "revision")
  const [redeployRevision, setRedeployRevision] = useState(selectedRevision?.number ?? deployment.revision_number ?? revisions[0]?.number ?? 1)
  const resourceSnapshot = {
    ...(selectedRevision ?? deployment),
    ...(resources?.template_id ? { template_id: resources.template_id } : {}),
    ...(resources?.template_name ? { template_name: resources.template_name } : {}),
    ...(resources?.template_cpu ? { template_cpu: resources.template_cpu } : {}),
    ...(resources?.template_memory_mb ? { template_memory_mb: resources.template_memory_mb } : {}),
    ...(resources?.template_disk_mb ? { template_disk_mb: resources.template_disk_mb } : {}),
    ...(resources?.sandbox_id ? { sandbox_id: resources.sandbox_id } : {}),
    ...(resources?.sandbox_region ? { sandbox_region: resources.sandbox_region } : {}),
    ...(resources?.expires_at ? { expires_at: resources.expires_at } : {}),
  }
  const formatLogTime = (value: string) => {
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return value
    const time = new Intl.DateTimeFormat(i18n.resolvedLanguage === "zh" ? "zh-CN" : "en", {
      hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
    }).format(parsed)
    return `${time}.${String(parsed.getMilliseconds()).padStart(3, "0")}`
  }
  return <div className="space-y-4">
    <Card className="gap-4 rounded-none border-0 border-b bg-transparent pb-4 pt-0 shadow-none">
      <CardHeader className="px-0">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="min-w-0 space-y-3">
            <CardTitle className="break-words text-2xl">{deployment.repository_full_name}</CardTitle>
            <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
              <DeploymentStatus status={deployment.status} />
              <span>{deployment.pull_request_number ? `#${deployment.pull_request_number}` : displayRevisionRef(deployment.ref) || deployment.commit_sha.slice(0, 8)}</span>
              {deployment.source_repository_full_name ? <span>{deployment.source_repository_full_name}:{deployment.source_ref}</span> : null}
              <span aria-hidden="true">·</span><code>{deployment.commit_sha.slice(0, 8)}</code>
            </div>
            <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-xs text-muted-foreground">
              <span>{t("user.deploymentCreatedAt")} {formatTime(deployment.created_at)}</span>
              {deployment.last_successful_revision_number ? <span>
                {t("user.deploymentLastSuccessful")} <button type="button" className="text-primary hover:underline" onClick={() => onSelectRevision(deployment.last_successful_revision_number!)}>#{deployment.last_successful_revision_number}</button>
              </span> : null}
            </div>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button type="button" size="sm" variant="outline" disabled={!canStop || Boolean(action)} onClick={() => onAction("stop")}><Square className="h-3.5 w-3.5" />{t("user.stopDeployment")}</Button>
            <Button type="button" size="sm" disabled={!canRetry || Boolean(action)} onClick={() => { setRedeployMode(deployment.source === "branch" || deployment.source === "pull_request" ? "latest" : "revision"); setRedeployRevision(selectedRevision?.number ?? deployment.revision_number ?? revisions[0]?.number ?? 1); setRedeployOpen(true) }}><RotateCcw className="h-3.5 w-3.5" />{t("user.retryDeployment")}</Button>
            <Button type="button" size="sm" variant="outline" disabled={Boolean(action)} onClick={() => onAction("delete")}><Trash2 className="h-3.5 w-3.5" />{t("common.delete")}</Button>
          </div>
        </div>
      </CardHeader>
      {deployment.endpoint ? <CardContent className="space-y-2 px-0">
        <a className="inline-flex items-center gap-2 break-all text-sm font-medium text-primary hover:underline" href={deployment.endpoint} target="_blank" rel="noopener noreferrer">{deployment.endpoint}<ExternalLink className="h-3.5 w-3.5 shrink-0" /></a>
        {deployment.endpoint_access === "public" ? <p className="text-xs text-muted-foreground">{t("user.deploymentEndpointPublic")}</p> : null}
      </CardContent> : null}
    </Card>
    <Card className="gap-3 rounded-none border-0 border-b bg-transparent pb-4 pt-0 shadow-none">
      <CardHeader className="px-0"><div className="flex items-center gap-2"><CardTitle>{t("user.deploymentResources")}</CardTitle>{resourcesLoading ? <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" aria-label={t("common.loading")} /> : null}</div><CardDescription>{t("user.deploymentResourcesDescription")}</CardDescription></CardHeader>
      <CardContent className="px-0">
        <dl className="grid gap-x-5 gap-y-3 sm:grid-cols-3 lg:grid-cols-7">
          <Info label={t("user.deploymentTemplate")} value={resourceSnapshot.template_name || resourceSnapshot.template_id || "—"} mono={!resourceSnapshot.template_name} />
          <Info label={t("user.deploymentCPU")} value={resourceSnapshot.template_cpu ? `${resourceSnapshot.template_cpu} vCPU` : "—"} />
          <Info label={t("user.deploymentMemory")} value={resourceSnapshot.template_memory_mb ? `${resourceSnapshot.template_memory_mb} MB` : "—"} />
          <Info label={t("user.deploymentDisk")} value={resourceSnapshot.template_disk_mb ? `${resourceSnapshot.template_disk_mb} MB` : "—"} />
          <Info label={t("user.deploymentSandboxRegion")} value={resourceSnapshot.sandbox_region || "—"} />
          <Info label={t("user.deploymentSandbox")} value={resourceSnapshot.sandbox_id || "—"} mono />
          <Info label={t("user.deploymentExpires")} value={formatTime(resourceSnapshot.expires_at)} />
        </dl>
      </CardContent>
    </Card>
    <div className="grid items-start lg:grid-cols-[280px_minmax(0,1fr)]">
      <Card className="min-w-0 gap-0 rounded-none border-0 border-b bg-muted/20 py-0 shadow-none lg:border-b-0 lg:border-r" data-testid="deployment-history">
        <CardHeader className="border-b px-4 py-3">
          <div className="flex items-start justify-between gap-2"><div className="space-y-2"><CardTitle>{t("user.deploymentHistory")}</CardTitle><CardDescription>{t("user.deploymentHistoryDescription")}</CardDescription></div><Badge variant="secondary">{revisions.length}</Badge></div>
        </CardHeader>
        <CardContent className="p-0">
          {revisions.map((revision) => <button key={revision.id} type="button" aria-pressed={revision.number === selectedRevision?.number} onClick={() => onSelectRevision(revision.number)} className={cn("flex w-full items-start gap-3 border-l-2 px-4 py-2.5 text-left transition-colors", revision.number === selectedRevision?.number ? "border-primary bg-primary/5" : "border-transparent hover:bg-accent")}>
            <span aria-hidden="true" className={cn("mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full", revision.status === "failed" ? "bg-destructive" : revision.status === "running" ? "bg-primary" : "bg-muted-foreground/50")} />
            <span className="min-w-0 flex-1 space-y-2">
              <span className="flex flex-wrap items-center gap-2 text-sm"><strong>{t("user.deploymentRevisionNumber", { number: revision.number })}</strong><DeploymentStatus status={revision.status} /></span>
              <span className="block text-xs text-muted-foreground">{formatTime(revision.created_at)}</span>
            </span>
            <code className="mt-1 text-xs text-muted-foreground">{revision.commit_sha.slice(0, 8)}</code>
          </button>)}
          {deployment.revisions_has_more ? <Button type="button" variant="ghost" onClick={onLoadOlderRevisions}>{t("user.deploymentHistoryOlder")}</Button> : null}
        </CardContent>
      </Card>
      <Tabs defaultValue="logs" className="min-w-0 gap-0 pt-3 lg:pl-6 lg:pt-0">
        <TabsList className="h-auto w-full justify-start gap-6 rounded-none border-b bg-transparent p-0 text-muted-foreground">
          <TabsTrigger className="h-10 flex-none rounded-none border-x-0 border-t-0 border-b-2 border-transparent bg-transparent px-0 py-2 text-sm font-medium shadow-none hover:text-foreground data-[state=active]:border-primary data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:shadow-none dark:data-[state=active]:bg-transparent" value="logs">{t("user.deploymentEvents")}</TabsTrigger>
          <TabsTrigger className="h-10 flex-none rounded-none border-x-0 border-t-0 border-b-2 border-transparent bg-transparent px-0 py-2 text-sm font-medium shadow-none hover:text-foreground data-[state=active]:border-primary data-[state=active]:bg-transparent data-[state=active]:text-foreground data-[state=active]:shadow-none dark:data-[state=active]:bg-transparent" value="terminal">{t("common.terminal")}</TabsTrigger>
        </TabsList>
        <TabsContent value="logs" className="m-0 pt-2">
      <Card className="min-w-0 gap-0 rounded-none border-0 bg-transparent py-0 shadow-none">
        <CardHeader className="border-b px-0 py-3">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0 space-y-2">
              <CardTitle>{t("user.deploymentEvents")}</CardTitle>
              {selectedRevision ? <CardDescription className="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span>{t("user.deploymentRevisionNumber", { number: selectedRevision.number })}</span>
                {selectedRevision.ref ? <><span aria-hidden="true">·</span><span>{t(selectedRevision.ref.startsWith("refs/pull/") ? "user.deploymentPullRequest" : selectedRevision.ref.startsWith("refs/tags/") ? "user.deploymentTag" : "user.deploymentBranch")} <code>{displayRevisionRef(selectedRevision.ref)}</code></span></> : null}
                <span aria-hidden="true">·</span><code className="break-all">{selectedRevision.commit_sha}</code>
              </CardDescription> : null}
            </div>
            <div className="flex shrink-0 items-center gap-2 text-xs text-muted-foreground">
              <span>{t("user.deploymentLogLines", { count: events.length })}</span>
              <Button type="button" variant="ghost" size="sm" disabled={loading} onClick={() => void refresh()}>{loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}{t("common.refresh")}</Button>
            </div>
          </div>
        </CardHeader>
        <CardContent className="space-y-3 px-0 pt-3">
          {selectedRevision?.failure_reason ? <p className="mx-4 rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{selectedRevision.failure_stage || t("user.deploymentFailure")}: {selectedRevision.failure_reason}</p> : null}
          {error ? <p role="alert" className="px-4 text-sm text-destructive">{t("user.deploymentEventsFailed")}: {error}</p> : null}
          {hasOlder ? <Button type="button" variant="outline" size="sm" className="ml-4" disabled={loading} onClick={() => void loadOlder()}>{t("user.deploymentEventsOlder")}</Button> : null}
          {events.length === 0 ? <p className="px-4 text-sm text-muted-foreground">{t("user.deploymentNoEvents")}</p> : <div data-testid="deployment-logs" className="deployment-console overflow-x-auto font-mono text-xs leading-5 sm:text-sm">
            {events.map((event, index) => <div data-testid="deployment-log-line" key={event.id} className={cn("grid grid-cols-[1.5rem_6rem_minmax(0,1fr)] gap-2 px-3 py-1 transition-colors sm:grid-cols-[2rem_7.5rem_minmax(0,1fr)] sm:px-4", event.event_type === "failure" ? "bg-destructive/5 text-destructive hover:bg-destructive/10" : event.stream === "stderr" ? "bg-amber-500/5 text-amber-700 hover:bg-amber-500/10 dark:text-amber-400" : "hover:bg-muted/50") }>
              <span aria-hidden="true" className="select-none text-right text-muted-foreground/60">{index + 1}</span>
              <time dateTime={event.created_at} title={`${formatTime(event.created_at)} · ${event.stage || event.event_type}${event.stream ? ` · ${event.stream}` : ""}`} className="whitespace-nowrap text-muted-foreground">{formatLogTime(event.created_at)}</time>
              <pre className="min-w-0 whitespace-pre-wrap break-words">{event.message}</pre>
            </div>)}
          </div>}
        </CardContent>
      </Card>
        </TabsContent>
        <TabsContent value="terminal" className="m-0 pt-2">
          {terminalAvailable ? <Suspense fallback={<p className="p-5 text-sm text-muted-foreground">{t("common.loading")}</p>}>
            <DeploymentTerminal key={`${deployment.id}:${deployment.revision_number}:${deployment.sandbox_id}`} endpoint={`/user/deployments/${encodeURIComponent(deployment.id)}/revisions/${deployment.revision_number}`} request={request} />
          </Suspense> : <div className="border-y bg-muted/20 px-4 py-12 text-center text-sm text-muted-foreground">{t("user.deploymentTerminalUnavailable")}</div>}
        </TabsContent>
      </Tabs>
    </div>
    <Dialog open={redeployOpen} onOpenChange={(open) => !action && setRedeployOpen(open)}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("user.redeployChooseTitle")}</DialogTitle>
          <DialogDescription>{t("user.redeployChooseDescription")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          {deployment.source === "branch" || deployment.source === "pull_request" ? <RedeployOption selected={redeployMode === "latest"} onSelect={() => setRedeployMode("latest")} title={t(deployment.source === "pull_request" ? "user.redeployLatestPullRequestTitle" : "user.redeployLatestTitle")} description={t(deployment.source === "pull_request" ? "user.redeployLatestPullRequestDescription" : "user.redeployLatestDescription")}>
            <Badge variant="secondary">{displayRevisionRef(deployment.ref) || deployment.commit_sha.slice(0, 8)}</Badge>
          </RedeployOption> : null}
          <RedeployOption selected={redeployMode === "revision"} onSelect={() => setRedeployMode("revision")} title={t("user.redeployRevisionTitle")} description={t("user.redeployRevisionDescription")}>
            <Badge variant="secondary">#{redeployRevision} · {revisions.find((revision) => revision.number === redeployRevision)?.commit_sha.slice(0, 8) ?? deployment.commit_sha.slice(0, 8)}</Badge>
          </RedeployOption>
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => setRedeployOpen(false)}>{t("common.cancel")}</Button>
          <Button type="button" disabled={Boolean(action)} onClick={() => { setRedeployOpen(false); onAction("retry", { mode: redeployMode, ...(redeployMode === "revision" ? { revision_number: redeployRevision } : {}) }) }}>{t("user.startRedeploy")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
}

function RedeployOption({ selected, onSelect, title, description, children }: { selected: boolean; onSelect: () => void; title: string; description: string; children: React.ReactNode }) {
  return <button type="button" onClick={onSelect} className={cn("flex w-full items-center gap-4 rounded-lg border p-4 text-left transition-colors", selected ? "border-primary bg-primary/5 ring-2 ring-primary/20" : "hover:bg-accent")}>
    <span aria-hidden="true" className={cn("h-5 w-5 shrink-0 rounded-full border-2 p-1", selected ? "border-primary" : "border-muted-foreground/40")}><span className={cn("block h-full w-full rounded-full", selected ? "bg-primary" : "bg-transparent")} /></span>
    <span className="min-w-0 flex-1"><span className="flex flex-wrap items-center gap-2 font-semibold">{title}{children}</span><span className="mt-1 block text-sm text-muted-foreground">{description}</span></span>
  </button>
}

function Info({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div><dt className="text-xs text-muted-foreground">{label}</dt><dd className={cn("mt-1 break-all text-sm", mono && "font-mono")}>{value}</dd></div>
}

function DeploymentStatus({ status }: { status: string }) {
  const { t } = useTranslation()
  const label: Record<string, string> = { queued: t("common.statusQueued"), planning: t("user.deploymentPlanning"), creating: t("common.statusCreating"), running: t("common.statusRunning"), stopping: t("common.statusStopping"), completed: t("common.statusCompleted"), expired: t("user.deploymentExpired"), failed: t("common.statusFailed") }
  return <Badge variant={status === "failed" ? "destructive" : status === "running" ? "default" : "secondary"}>{label[status] || status}</Badge>
}

function useDeploymentEvents(request: Request, id: string | null, revisionNumber?: number, status?: string, login?: string) {
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
  }, [id, revisionNumber, request, login])

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
        const endpoint = revisionNumber ? `/user/deployments/${encodeURIComponent(id)}/revisions/${revisionNumber}/events` : `/user/deployments/${encodeURIComponent(id)}/events`
        const page = await request(`${endpoint}${query}`) as EventPage
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
  }, [id, revisionNumber, request])

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

function revisionNumberFromURL(): number | undefined {
  if (typeof window === "undefined") return undefined
  const value = new URLSearchParams(window.location.search).get("revision")
  if (!value || !/^[1-9]\d*$/.test(value)) return undefined
  const number = Number(value)
  return Number.isSafeInteger(number) ? number : undefined
}

function displayRevisionRef(ref?: string) { return ref?.replace(/^refs\/pull\/(\d+)\/head$/, "#$1").replace(/^refs\/(heads|tags)\//, "") ?? "" }
