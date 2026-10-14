import { describe, expect, test } from "bun:test"
import { deploymentCreatePath, deploymentPrefillFromSearch, jobDeploymentContext, deploymentContext, deploymentListAPIPath, deploymentPath, githubContextFromPath, githubContextPath } from "./github-context-routes"
import { appRouteAccess, deploymentIDFromPath, signInURL, userDataResources } from "./app-load-policy"

describe("GitHub source context routes", () => {
  test("Jobs and deployments round trip the same source with slash and Unicode names", () => {
    for (const kind of ["branches", "tags", "pulls"]) {
      const source = { kind, repository: "qiniu/deployment", reference: kind === "pulls" ? "125" : "a".repeat(40), ...(kind === "pulls" ? {} : { name: "feature/中文 #1" }) }
      for (const view of kind === "tags" ? ["deployments"] : ["jobs", "deployments"]) {
        const path = githubContextPath(source, view, view === "jobs" ? { job: "j1" } : { deployment: "d1", revision: 2 })
        const url = new URL(path, "https://example.com")
        expect(githubContextFromPath(url.pathname, url.search)).toEqual(source)
        if (view === "deployments") {
          expect(appRouteAccess(url.pathname)).toBe("user")
          expect(deploymentIDFromPath(url.pathname, url.search)).toBe("d1")
          expect(userDataResources(url.pathname)).toEqual([])
          expect(decodeURIComponent(signInURL(url.pathname, url.search))).toContain(path)
        }
      }
    }
  })
  test("canonicalizes by saved revision evidence and retains legacy untyped refs", () => {
    const deployment = { id: "d1", repository_full_name: "qiniu/deployment", source: "branch", ref: "main", commit_sha: "b".repeat(40) }
    const revision = { number: 1, ref: "refs/heads/example/vite", commit_sha: "a".repeat(40) }
    expect(deploymentPath(deployment, revision)).toBe(`/github/qiniu/deployment/branch/example%2Fvite/deployments?commit=${"a".repeat(40)}&deployment=d1&revision=1`)
    expect(deploymentPath({ ...deployment, source: undefined, ref: "legacy" })).toBe("/deployments/d1")
    expect(deploymentListAPIPath(deploymentContext(deployment, revision))).toContain("ref=refs%2Fheads%2Fexample%2Fvite")
    expect(deploymentPath({ ...deployment, pull_request_number: 125 }, revision)).toBe("/github/qiniu/deployment/pr/125/deployments?deployment=d1&revision=1")
  })
  test("rejects malformed source paths and keeps creation separate", () => {
    for (const path of ["/github/pulls/o/r/0/deployments", "/github/pulls/o/r/no/deployments", "/github/branches/o%2Fr/r/a/deployments", "/github/tags/o/r/%zz/deployments", "/github/pulls/o/r/1/deployments/more"]) expect(githubContextFromPath(path)).toBeNull()
    expect(deploymentIDFromPath("/deployments/new")).toBeUndefined()
    expect(deploymentIDFromPath("/deployments/old-id", "?revision=2")).toBe("old-id")
  })
})

test("repository-first routes keep source history independent from immutable commit selection", () => {
  const context = githubContextFromPath("/github/qiniu/deployment/branch/example%2Fvite/deployments")
  expect(context).toEqual({ kind: "branches", repository: "qiniu/deployment", name: "example/vite", reference: "" })
  expect(deploymentListAPIPath(context)).toBe("/user/deployments?repository_full_name=qiniu%2Fdeployment&ref=refs%2Fheads%2Fexample%2Fvite")
  expect(githubContextFromPath("/github/pulls/qiniu/deployment/7/jobs")).toEqual(githubContextFromPath("/github/qiniu/deployment/pr/7/jobs"))
  expect(githubContextFromPath("/github/qiniu/deployment/pr/0/jobs")).toBeNull()
  expect(githubContextFromPath("/github/qiniu/deployment/branch/%zz/jobs")).toBeNull()
  expect(githubContextFromPath("/github/qiniu/deployment/run/1/deployments")).toBeNull()
})

test("Job creation links pin the selected Job, including older PR commits", () => {
  const job = { repository_full_name: "qiniu/deployment", pull_request_number: 7, head_sha: "a".repeat(40), head_branch: "feature/中文" }
  const context = jobDeploymentContext(job)
  expect(githubContextPath(context, "deployments")).toBe("/github/qiniu/deployment/pr/7/deployments")
  const url = new URL(deploymentCreatePath(context, job.head_sha), "https://example.com")
  expect(deploymentPrefillFromSearch(url.search)).toEqual({ repository: job.repository_full_name, source: "pull_request", ref: "7", commitSHA: job.head_sha })
  expect(deploymentPrefillFromSearch("?repository=o/r&source=pull_request&ref=7&commit_sha=not-a-sha")).toBeNull()
  expect(jobDeploymentContext({ ...job, head_sha: "" })).toBeNull()
  const branch = jobDeploymentContext({ ...job, pull_request_number: 0 })
  const branchURL = new URL(deploymentCreatePath(branch), "https://example.com")
  expect(deploymentPrefillFromSearch(branchURL.search).ref).toBe("feature/中文")
})
