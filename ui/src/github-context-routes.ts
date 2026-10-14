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

export function githubContextFromPath(path: string, search = ""): GitHubContext | null {
  const match = path.match(/^\/github\/(pulls|branches|tags|runs)\/([^/]+)\/([^/]+)\/([^/]+)\/(jobs|deployments)$/)
  if (!match || (match[1] === "runs" && match[5] === "deployments")) return null
  const owner = decode(match[2]), repository = decode(match[3]), reference = decode(match[4])
  if (!owner || !repository || !reference || /[/\\]/.test(owner + repository)) return null
  const kind = match[1] as GitHubContext["kind"]
  if ((kind === "pulls" || kind === "runs") && !/^[1-9]\d*$/.test(reference)) return null
  const name = new URLSearchParams(search).get(kind === "tags" ? "tag" : "branch") || undefined
  return { kind, repository: `${owner}/${repository}`, reference, ...((kind === "branches" || kind === "tags") && name ? { name } : {}) }
}

export function githubContextPath(context: GitHubContext, view: "jobs" | "deployments", selection: Record<string, string | number | undefined> = {}): string {
  const repository = context.repository.split("/").map(encodeURIComponent).join("/")
  const query = new URLSearchParams()
  if (context.name) query.set(context.kind === "tags" ? "tag" : "branch", context.name)
  for (const [key, value] of Object.entries(selection)) if (value !== undefined) query.set(key, String(value))
  return `/github/${context.kind}/${repository}/${encodeURIComponent(context.reference)}/${view}${query.size ? `?${query}` : ""}`
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
    query.set("commit_sha", context.reference)
    if (context.name) query.set("ref", `refs/${context.kind === "tags" ? "tags" : "heads"}/${context.name}`)
  }
  return `/user/deployments?${query}`
}
