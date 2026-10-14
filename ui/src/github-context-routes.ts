// Jobs and deployments share repository/source identity; selection stays in the query.
export type GitHubContext = {
  kind: "pulls" | "branches" | "tags" | "runs"
  repository: string
  reference: string
  name?: string
}

function decode(value: string): string | null {
  try {
    const result = decodeURIComponent(value)
    return result && ![...result].some((character) => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127) ? result : null
  } catch { return null }
}

const routeKinds = { pr: "pulls", branch: "branches", tag: "tags", run: "runs" } as const
const routeSegments = { pulls: "pr", branches: "branch", tags: "tag", runs: "run" } as const

export function githubContextFromPath(path: string, search = ""): GitHubContext | null {
  const current = path.match(/^\/github\/([^/]+)\/([^/]+)\/(pr|branch|tag|run)\/([^/]+)\/(jobs|deployments)$/)
  const legacy = current ? null : path.match(/^\/github\/(pulls|branches|tags|runs)\/([^/]+)\/([^/]+)\/([^/]+)\/(jobs|deployments)$/)
  if (!current && !legacy) return null
  const kind = current ? routeKinds[current[3] as keyof typeof routeKinds] : legacy![1] as GitHubContext["kind"]
  const view = (current ?? legacy)![5]
  if ((kind === "runs" && view === "deployments") || (kind === "tags" && view === "jobs")) return null
  const owner = decode(current ? current[1] : legacy![2])
  const repository = decode(current ? current[2] : legacy![3])
  const value = decode((current ?? legacy)![4])
  if (!owner || !repository || !value || /[/\\]/.test(owner + repository) || [owner, repository].some((part) => part === "." || part === "..")) return null
  if ((kind === "pulls" || kind === "runs") && !/^[1-9]\d*$/.test(value)) return null
  const query = new URLSearchParams(search)
  if (kind === "branches" || kind === "tags") {
    const name = current ? value : query.get(kind === "tags" ? "tag" : "branch") || undefined
    const reference = current ? query.get("commit") || "" : value
    return { kind, repository: `${owner}/${repository}`, reference, ...(name ? { name } : {}) }
  }
  return { kind, repository: `${owner}/${repository}`, reference: value }
}

export function githubContextPath(context: GitHubContext, view: "jobs" | "deployments", selection: Record<string, string | number | undefined> = {}): string {
  const repository = context.repository.split("/").map(encodeURIComponent).join("/")
  const query = new URLSearchParams()
  const named = context.kind === "branches" || context.kind === "tags"
  if (named && context.reference) query.set("commit", context.reference)
  for (const [key, value] of Object.entries(selection)) if (value !== undefined) query.set(key, String(value))
  // Old SHA-only branch links lack a ref name; preserve their readable identity.
  if (named && !context.name) {
    query.delete("commit")
    return `/github/${context.kind}/${repository}/${encodeURIComponent(context.reference)}/${view}${query.size ? `?${query}` : ""}`
  }
  return `/github/${repository}/${routeSegments[context.kind]}/${encodeURIComponent(named ? context.name! : context.reference)}/${view}${query.size ? `?${query}` : ""}`
}

type DeploymentSource = {
  id: string
  repository_full_name: string
  source?: string
  ref?: string
  commit_sha: string
  revision_number?: number
  pull_request_number?: number
}
type RevisionSource = { ref?: string; commit_sha: string; number: number }

export function deploymentContext(deployment: DeploymentSource, revision?: RevisionSource | null): GitHubContext | null {
  if (deployment.pull_request_number) return { kind: "pulls", repository: deployment.repository_full_name, reference: String(deployment.pull_request_number) }
  const ref = revision?.ref ?? deployment.ref ?? ""
  const kind = ref.startsWith("refs/tags/") || deployment.source === "tag" ? "tags" : ref.startsWith("refs/heads/") || deployment.source === "branch" ? "branches" : null
  if (!kind || !deployment.commit_sha) return null
  return { kind, repository: deployment.repository_full_name, reference: revision?.commit_sha ?? deployment.commit_sha, name: ref.replace(/^refs\/(heads|tags)\//, "") }
}

export function deploymentPath(deployment: DeploymentSource, revision?: RevisionSource | null, explicitRevision?: number): string {
  const context = deploymentContext(deployment, revision)
  const number = explicitRevision ?? revision?.number
  if (!context) return `/deployments/${encodeURIComponent(deployment.id)}${number ? `?revision=${number}` : ""}`
  return githubContextPath(context, "deployments", { deployment: deployment.id, revision: number })
}

export function deploymentListAPIPath(context: GitHubContext | null): string {
  if (!context) return "/user/deployments"
  const query = new URLSearchParams({ repository_full_name: context.repository })
  if (context.kind === "pulls") query.set("pull_request_number", context.reference)
  else {
    if (context.reference) query.set("commit_sha", context.reference)
    if (context.name) query.set("ref", `refs/${context.kind === "tags" ? "tags" : "heads"}/${context.name}`)
  }
  return `/user/deployments?${query}`
}

export type DeploymentPrefill = { repository: string; source: "branch" | "tag" | "pull_request"; ref: string; commitSHA: string }

export function deploymentCreatePath(context: GitHubContext | null, commitSHA = ""): string {
  if (!context || context.kind === "runs") return "/deployments/new"
  const ref = context.kind === "pulls" ? context.reference : context.name
  if (!ref) return "/deployments/new"
  const query = new URLSearchParams({ repository: context.repository, source: context.kind === "pulls" ? "pull_request" : context.kind === "tags" ? "tag" : "branch", ref })
  const sha = commitSHA || (context.kind !== "pulls" ? context.reference : "")
  if (sha) query.set("commit_sha", sha)
  return `/deployments/new?${query}`
}

export function deploymentPrefillFromSearch(search: string): DeploymentPrefill | null {
  const query = new URLSearchParams(search)
  const repository = query.get("repository") || ""
  const source = query.get("source")
  const ref = query.get("ref") || ""
  const commitSHA = query.get("commit_sha") || ""
  if (!/^[^/\\?#%\s]+\/[^/\\?#%\s]+$/.test(repository) || !["branch", "tag", "pull_request"].includes(source || "") || !ref || [...ref].some((character) => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127)) return null
  if (source === "pull_request" && !/^[1-9]\d*$/.test(ref)) return null
  if (commitSHA && !/^[a-f0-9]{40}$/.test(commitSHA)) return null
  return { repository, source: source as DeploymentPrefill["source"], ref, commitSHA }
}

export function jobDeploymentContext(job: { repository_full_name?: string; pull_request_number?: number; head_branch?: string; head_sha?: string }): GitHubContext | null {
  if (!job.repository_full_name || !/^[a-f0-9]{40}$/.test(job.head_sha || "")) return null
  if (job.pull_request_number && job.pull_request_number > 0) return { kind: "pulls", repository: job.repository_full_name, reference: String(job.pull_request_number) }
  if (job.head_branch) return { kind: "branches", repository: job.repository_full_name, name: job.head_branch, reference: job.head_sha! }
  return null
}
