import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  AlertCircle,
  CheckCircle2,
  Clock3,
  Copy,
  Loader2,
  Play,
  Plus,
  RefreshCw,
  Square,
  Trash2,
} from "lucide-react"
import { toast } from "sonner"

import { AppSidebar } from "@/components/app-sidebar"
import { SiteHeader } from "@/components/site-header"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Toaster } from "@/components/ui/sonner"
import { cn } from "@/lib/utils"

type RunnerStatus = "queued" | "creating" | "running" | "stopping" | "stopped" | "failed"

type RunnerState = {
  id: string
  status: RunnerStatus
  runner_name: string
  sandbox_id?: string
  process_pid?: number
  assigned_job_id?: number
  assigned_job_name?: string
  error?: string
  updated_at: string
  created_at: string
  stopped_at?: string
}

type Metric = {
  label: string
  value: number
  description: string
}

const tokenKey = "runnerd-admin-token"
const activeStatuses = new Set<RunnerStatus>(["queued", "creating", "running", "stopping"])
const logNames = ["control.log", "stdout.log", "stderr.log"] as const

function App() {
  const [token, setTokenState] = useState(() => localStorage.getItem(tokenKey) || "")
  const [tokenInput, setTokenInput] = useState("")
  const [runners, setRunners] = useState<RunnerState[]>([])
  const [selectedID, setSelectedID] = useState("")
  const [selectedLog, setSelectedLog] = useState<(typeof logNames)[number]>("control.log")
  const [logText, setLogText] = useState("No runner selected")
  const [loading, setLoading] = useState(false)
  const [connected, setConnected] = useState(false)
  const [lastUpdated, setLastUpdated] = useState("")
  const [createID, setCreateID] = useState("")
  const [createLabels, setCreateLabels] = useState("self-hosted,e2b")
  const createIDRef = useRef<HTMLInputElement>(null)

  const selected = useMemo(
    () => runners.find((runner) => runner.id === selectedID),
    [runners, selectedID]
  )

  const metrics = useMemo<Metric[]>(() => {
    const count = (status: RunnerStatus) => runners.filter((runner) => runner.status === status).length
    return [
      {
        label: "Active",
        value: runners.filter((runner) => activeStatuses.has(runner.status)).length,
        description: "queued / creating / running",
      },
      { label: "Running", value: count("running"), description: "attached to GitHub" },
      { label: "Failed", value: count("failed"), description: "needs inspection" },
      { label: "Stopped", value: count("stopped"), description: "cleaned or recovered" },
    ]
  }, [runners])

  const setToken = useCallback((value: string) => {
    const next = value.trim()
    setTokenState(next)
    if (next) localStorage.setItem(tokenKey, next)
    else localStorage.removeItem(tokenKey)
  }, [])

  const request = useCallback(
    async (url: string, options: RequestInit = {}) => {
      const headers = new Headers(options.headers)
      headers.set("Authorization", `Bearer ${token}`)
      const response = await fetch(url, { ...options, headers })
      if (response.status === 401) {
        setToken("")
        setConnected(false)
        throw new Error("Invalid ADMIN_TOKEN")
      }
      if (!response.ok) {
        const text = await response.text()
        throw new Error(text || `${response.status} ${response.statusText}`)
      }
      const contentType = response.headers.get("content-type") || ""
      if (contentType.includes("application/json")) return response.json()
      return response.text()
    },
    [setToken, token]
  )

  const loadLog = useCallback(
    async (id: string, name: (typeof logNames)[number]) => {
      if (!token || !id) {
        setLogText("No runner selected")
        return
      }
      setLogText("Loading...")
      try {
        const text = (await request(
          `/runners/${encodeURIComponent(id)}/logs/${encodeURIComponent(name)}`
        )) as string
        setLogText(text || "Log is empty")
      } catch (error) {
        setLogText(error instanceof Error ? error.message : "Failed to load log")
      }
    },
    [request, token]
  )

  const loadRunners = useCallback(async () => {
    if (!token) {
      setConnected(false)
      return
    }
    setLoading(true)
    try {
      const data = (await request("/runners")) as RunnerState[]
      setRunners(Array.isArray(data) ? data : [])
      setConnected(true)
      setLastUpdated(new Date().toLocaleTimeString())
      if (selectedID && !data.some((runner) => runner.id === selectedID)) {
        setSelectedID("")
        setLogText("No runner selected")
      }
    } catch (error) {
      setConnected(false)
      toast.error(error instanceof Error ? error.message : "Failed to load runners")
    } finally {
      setLoading(false)
    }
  }, [request, selectedID, token])

  useEffect(() => {
    void fetch("/healthz").catch(() => setConnected(false))
  }, [])

  useEffect(() => {
    void loadRunners()
    const timer = window.setInterval(() => void loadRunners(), 5000)
    return () => window.clearInterval(timer)
  }, [loadRunners])

  useEffect(() => {
    if (selectedID) void loadLog(selectedID, selectedLog)
  }, [loadLog, selectedID, selectedLog])

  const submitToken = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setToken(tokenInput)
    setTokenInput("")
    toast.success("Admin token saved")
  }

  const clearToken = () => {
    setToken("")
    setRunners([])
    setSelectedID("")
    setLogText("No runner selected")
  }

  const createRunner = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!token) {
      toast.error("ADMIN_TOKEN required")
      return
    }
    const labels = createLabels
      .split(",")
      .map((label) => label.trim())
      .filter(Boolean)
    const body: { id?: string; labels?: string[] } = {}
    if (createID.trim()) body.id = createID.trim()
    if (labels.length > 0) body.labels = labels
    try {
      const runner = (await request("/runners", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      })) as RunnerState
      setCreateID("")
      setSelectedID(runner.id)
      toast.success(`Runner ${runner.id} queued`)
      await loadRunners()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Failed to create runner")
    }
  }

  const stopRunner = async (id: string) => {
    try {
      const runner = (await request(`/runners/${encodeURIComponent(id)}`, {
        method: "DELETE",
      })) as RunnerState
      setSelectedID(runner.id)
      toast.success(`Runner ${runner.id} stopped`)
      await loadRunners()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Failed to stop runner")
    }
  }

  const copySelectedID = async () => {
    if (!selected) return
    await navigator.clipboard.writeText(selected.id)
    toast.success("Runner ID copied")
  }

  return (
    <SidebarProvider>
      <AppSidebar
        connected={connected}
        activeCount={metrics[0]?.value || 0}
        onRefresh={() => void loadRunners()}
        onCreateFocus={() => createIDRef.current?.focus()}
        onClearToken={clearToken}
      />
      <SidebarInset>
        <SiteHeader />
        <main className="flex flex-1 flex-col gap-4 p-4 lg:gap-6 lg:p-6">
          {!token ? (
            <Card className="max-w-xl">
              <CardHeader>
                <CardTitle>Admin access</CardTitle>
                <CardDescription>Use the runnerd ADMIN_TOKEN.</CardDescription>
              </CardHeader>
              <CardContent>
                <form className="flex flex-col gap-3 sm:flex-row" onSubmit={submitToken}>
                  <Input
                    type="password"
                    value={tokenInput}
                    onChange={(event) => setTokenInput(event.target.value)}
                    placeholder="ADMIN_TOKEN"
                    autoComplete="current-password"
                  />
                  <Button type="submit">Connect</Button>
                </form>
              </CardContent>
            </Card>
          ) : null}

          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            {metrics.map((metric) => (
              <Card key={metric.label} className="gap-3 py-5">
                <CardHeader className="px-5">
                  <CardDescription>{metric.label}</CardDescription>
                  <CardTitle className="text-3xl">{metric.value}</CardTitle>
                </CardHeader>
                <CardContent className="px-5 text-xs text-muted-foreground">
                  {metric.description}
                </CardContent>
              </Card>
            ))}
          </div>

          <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_420px]">
            <Card className="min-w-0 gap-0 py-0">
              <CardHeader className="border-b px-5 py-4">
                <div className="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
                  <div>
                    <CardTitle>Runners</CardTitle>
                    <CardDescription>
                      {lastUpdated ? `Updated ${lastUpdated}` : "Waiting for runner state"}
                    </CardDescription>
                  </div>
                  <form className="flex flex-col gap-2 sm:flex-row" onSubmit={createRunner}>
                    <Input
                      ref={createIDRef}
                      className="sm:w-44"
                      value={createID}
                      onChange={(event) => setCreateID(event.target.value)}
                      placeholder="optional id"
                    />
                    <Input
                      className="sm:w-56"
                      value={createLabels}
                      onChange={(event) => setCreateLabels(event.target.value)}
                      placeholder="self-hosted,e2b"
                    />
                    <Button type="submit" disabled={!token}>
                      <Plus />
                      Create
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      size="icon"
                      onClick={() => void loadRunners()}
                      disabled={loading}
                      title="Refresh"
                    >
                      <RefreshCw className={cn(loading && "animate-spin")} />
                    </Button>
                  </form>
                </div>
              </CardHeader>
              <CardContent className="p-0">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Status</TableHead>
                      <TableHead>Runner</TableHead>
                      <TableHead>Sandbox</TableHead>
                      <TableHead>Job</TableHead>
                      <TableHead>Updated</TableHead>
                      <TableHead className="w-24" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {runners.length === 0 ? (
                      <TableRow>
                        <TableCell colSpan={6} className="h-24 text-center text-muted-foreground">
                          No runner requests found
                        </TableCell>
                      </TableRow>
                    ) : (
                      runners.map((runner) => (
                        <TableRow
                          key={runner.id}
                          data-state={runner.id === selectedID ? "selected" : undefined}
                          className="cursor-pointer"
                          onClick={() => setSelectedID(runner.id)}
                        >
                          <TableCell>
                            <StatusBadge status={runner.status} />
                          </TableCell>
                          <TableCell>
                            <div className="font-medium">{runner.runner_name || runner.id}</div>
                            <div className="text-xs text-muted-foreground">{runner.id}</div>
                          </TableCell>
                          <TableCell className="max-w-[180px] truncate">
                            {runner.sandbox_id || "-"}
                          </TableCell>
                          <TableCell className="max-w-[180px] truncate">
                            {runner.assigned_job_name || runner.assigned_job_id || "-"}
                          </TableCell>
                          <TableCell>{formatTime(runner.updated_at)}</TableCell>
                          <TableCell>
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              onClick={(event) => {
                                event.stopPropagation()
                                void stopRunner(runner.id)
                              }}
                            >
                              <Trash2 />
                              Stop
                            </Button>
                          </TableCell>
                        </TableRow>
                      ))
                    )}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>

            <Card className="min-w-0 gap-0 py-0">
              <CardHeader className="border-b px-5 py-4">
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <CardTitle>Details</CardTitle>
                    <CardDescription>{selected?.runner_name || "Select a runner"}</CardDescription>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    onClick={() => void copySelectedID()}
                    disabled={!selected}
                    title="Copy runner ID"
                  >
                    <Copy />
                  </Button>
                </div>
              </CardHeader>
              {selected ? (
                <CardContent className="grid gap-4 p-5">
                  <div className="grid grid-cols-[110px_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm">
                    <Detail label="ID" value={selected.id} />
                    <Detail label="Status" value={selected.status} />
                    <Detail label="Sandbox" value={selected.sandbox_id || "-"} />
                    <Detail label="PID" value={selected.process_pid || "-"} />
                    <Detail
                      label="Job"
                      value={selected.assigned_job_name || selected.assigned_job_id || "-"}
                    />
                    <Detail label="Created" value={formatTime(selected.created_at)} />
                    <Detail label="Updated" value={formatTime(selected.updated_at)} />
                    <Detail label="Stopped" value={formatTime(selected.stopped_at)} />
                    <Detail label="Error" value={selected.error || "-"} />
                  </div>
                  <Tabs
                    value={selectedLog}
                    onValueChange={(value) => setSelectedLog(value as (typeof logNames)[number])}
                  >
                    <TabsList>
                      {logNames.map((name) => (
                        <TabsTrigger key={name} value={name}>
                          {name.replace(".log", "")}
                        </TabsTrigger>
                      ))}
                    </TabsList>
                  </Tabs>
                  <pre className="max-h-[48vh] min-h-72 overflow-auto rounded-lg border bg-muted/50 p-3 text-xs leading-relaxed whitespace-pre-wrap">
                    {logText}
                  </pre>
                </CardContent>
              ) : (
                <CardContent className="p-8 text-sm text-muted-foreground">
                  No runner selected
                </CardContent>
              )}
            </Card>
          </div>
        </main>
      </SidebarInset>
      <Toaster richColors />
    </SidebarProvider>
  )
}

function StatusBadge({ status }: { status: RunnerStatus }) {
  if (status === "running") {
    return (
      <Badge variant="success">
        <Play />
        running
      </Badge>
    )
  }
  if (status === "failed") {
    return (
      <Badge variant="danger">
        <AlertCircle />
        failed
      </Badge>
    )
  }
  if (status === "stopped") {
    return (
      <Badge variant="outline">
        <Square />
        stopped
      </Badge>
    )
  }
  if (status === "creating") {
    return (
      <Badge variant="warning">
        <Loader2 className="animate-spin" />
        creating
      </Badge>
    )
  }
  if (status === "stopping") {
    return (
      <Badge variant="warning">
        <Clock3 />
        stopping
      </Badge>
    )
  }
  return (
    <Badge variant="secondary">
      <CheckCircle2 />
      queued
    </Badge>
  )
}

function Detail({ label, value }: { label: string; value: string | number }) {
  return (
    <>
      <div className="text-muted-foreground">{label}</div>
      <div className="min-w-0 break-words font-medium">{value}</div>
    </>
  )
}

function formatTime(value?: string) {
  if (!value) return "-"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "-"
  return date.toLocaleString()
}

export default App
