import { describe, expect, test } from "bun:test"
import { deploymentContext, deploymentListAPIPath, deploymentPath, githubContextFromPath, githubContextPath } from "./github-context-routes"
import { appRouteAccess, deploymentIDFromPath, signInURL, userDataResources } from "./app-load-policy"

describe("GitHub source context routes", () => {
  test("Jobs and deployments round trip the same source with slash and Unicode names", () => {
    for (const kind of ["branches", "tags", "pulls"]) {
      const source = { kind, repository: "qiniu/deployment", reference: kind === "pulls" ? "125" : "a".repeat(40), ...(kind === "pulls" ? {} : { name: "feature/中文 #1" }) }
      for (const view of ["jobs", "deployments"]) {
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
    expect(deploymentPath(deployment, revision)).toBe(`/github/branches/qiniu/deployment/${"a".repeat(40)}/deployments?branch=example%2Fvite&deployment=d1&revision=1`)
    expect(deploymentPath({ ...deployment, source: undefined, ref: "legacy" })).toBe("/deployments/d1")
    expect(deploymentListAPIPath(deploymentContext(deployment, revision))).toContain("ref=refs%2Fheads%2Fexample%2Fvite")
    expect(deploymentPath({ ...deployment, pull_request_number: 125 }, revision)).toBe("/github/pulls/qiniu/deployment/125/deployments?deployment=d1&revision=1")
  })
  test("rejects malformed source paths and keeps creation separate", () => {
    for (const path of ["/github/pulls/o/r/0/deployments", "/github/pulls/o/r/no/deployments", "/github/branches/o%2Fr/r/a/deployments", "/github/tags/o/r/%zz/deployments", "/github/pulls/o/r/1/deployments/more"]) expect(githubContextFromPath(path)).toBeNull()
    expect(deploymentIDFromPath("/deployments/new")).toBeUndefined()
    expect(deploymentIDFromPath("/deployments/old-id", "?revision=2")).toBe("old-id")
  })
})
