import { expect, test, type Page } from "@playwright/test"

import type { RunnerJobGroup, RunnerState } from "../src/admin-types"
import { getLocalAuthSessionRoute } from "./production-smoke-support"

const postRenderObservationMs = 1_000

test("boots the public landing page from the production bundle", async ({ page }) => {
  const diagnostics = observeBrowserDiagnostics(page)

  await routeLocalAnonymousSession(page)

  const response = await page.goto("/", { waitUntil: "networkidle" })

  expect(response?.ok()).toBe(true)
  await expect(
    page.getByRole("heading", { name: "GitHub Actions, powered by Qiniu Sandbox" }),
  ).toBeVisible()
  await expect(page.locator("#root")).not.toBeEmpty()
  await page.waitForTimeout(postRenderObservationMs)
  diagnostics.expectClean()
})

test("serves the hosted guide as a responsive public production route", async ({ page }) => {
  const diagnostics = observeBrowserDiagnostics(page)

  await routeLocalAnonymousSession(page)
  await page.setViewportSize({ width: 390, height: 844 })
  const response = await page.goto("/docs/getting-started/hosted", { waitUntil: "networkidle" })

  expect(response?.ok()).toBe(true)
  await expect(
    page.getByRole("heading", { name: "Get started with the hosted service", exact: true }),
  ).toBeVisible()
  await expect(page.getByRole("link", { name: "Copy a complete workflow" })).toHaveAttribute(
    "href",
    "/docs/guides/workflow",
  )
  await expect(page.locator("#root")).not.toBeEmpty()

  const viewport = await page.evaluate(() => ({
    documentWidth: document.documentElement.scrollWidth,
    viewportWidth: window.innerWidth,
  }))
  expect(viewport.documentWidth).toBeLessThanOrEqual(viewport.viewportWidth + 1)

  await page.waitForTimeout(postRenderObservationMs)
  diagnostics.expectClean()
})

test("serves the custom template guide from the production bundle", async ({ page }) => {
  const diagnostics = observeBrowserDiagnostics(page)

  await routeLocalAnonymousSession(page)
  await page.setViewportSize({ width: 390, height: 844 })
  const response = await page.goto("/docs/guides/custom-templates", { waitUntil: "networkidle" })

  expect(response?.ok()).toBe(true)
  await expect(
    page.getByRole("heading", { name: "Build and use a custom runner template", exact: true }),
  ).toBeVisible()
  await expect(page).toHaveTitle("Build and use a custom runner template · Qiniu CI Runner")
  await expect(page.locator('meta[name="description"]')).toHaveAttribute(
    "content",
    "Build a private Qiniu Sandbox template for your own tools, connect it to a custom Runner Spec, and select it from a GitHub Actions workflow.",
  )
  await expect(
    page.locator("pre code").filter({ hasText: "qshell sandbox template build --wait" }),
  ).toBeVisible()
  await expect(page.getByText("Status: ready", { exact: true }).first()).toBeVisible()

  const viewport = await page.evaluate(() => ({
    documentWidth: document.documentElement.scrollWidth,
    viewportWidth: window.innerWidth,
  }))
  expect(viewport.documentWidth).toBeLessThanOrEqual(viewport.viewportWidth + 1)

  await page.waitForTimeout(postRenderObservationMs)
  diagnostics.expectClean()
})

test("keeps the Jobs list independently scrollable beside the Web Console", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")

  const diagnostics = observeBrowserDiagnostics(page)
  const runners = fixtureRunners(40)
  const selected = runners[0]
  const selectedGroup: RunnerJobGroup = {
    key: `branch:${selected.repository_full_name}:${selected.head_branch}:${selected.head_sha}`,
    group: "branch",
    repository: selected.repository_full_name || "fixture/repository-0",
    title: selected.head_branch || "fixture-branch-0",
    subtitle: selected.head_sha || "0".repeat(40),
    updated_at: selected.updated_at,
    jobs: [selected],
    current_jobs: [selected],
    previous_jobs: [],
    workflow_run_ids: [selected.workflow_run_id || 1],
    head_sha: selected.head_sha,
    head_branch: selected.head_branch,
  }

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.route("**/auth/session", async (route) => {
    await route.fulfill({
      json: {
        authenticated: true,
        oauth_enabled: true,
        login: "fixture-user",
        role: "user",
      },
    })
  })
  await page.route("**/user/**", async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname === "/user/github-app") {
      await route.fulfill({
        json: {
          setup_url: "/github-app/setup",
          installations: [{
            id: 1,
            account_id: 1,
            installation_id: 101,
            account_login: "fixture-user",
            repositories: runners.map((runner) => runner.repository_full_name),
            created_at: "2026-08-12T00:00:00Z",
            updated_at: "2026-08-12T00:00:00Z",
          }],
        },
      })
      return
    }
    if (url.pathname === "/user/runner_requests") {
      await route.fulfill({
        headers: { "X-Total-Count": String(runners.length) },
        json: runners,
      })
      return
    }
    if (url.pathname === "/user/onboarding/product-tour") {
      await route.fulfill({
        json: { version: 1, status: "completed", tour_seen: true },
      })
      return
    }
    if (url.pathname.startsWith("/user/github/branches/")) {
      await route.fulfill({ json: selectedGroup })
      return
    }
    if (url.pathname.endsWith("/events")) {
      await route.fulfill({ json: fixtureRunnerEvents() })
      return
    }
    await route.fulfill({ status: 404, body: "fixture route not found" })
  })

  const response = await page.goto("/jobs", { waitUntil: "networkidle" })
  expect(response?.ok()).toBe(true)

  const jobList = page.locator("main aside .overflow-y-auto")
  const webConsoleTab = page.getByRole("tab", { name: /Web Console|Web 控制台/ })
  await expect(jobList).toBeVisible()
  await expect(webConsoleTab).toBeVisible()
  await webConsoleTab.click()

  const webConsole = page.getByRole("tabpanel", { name: /Web Console|Web 控制台/ })
  await expect(webConsole).toBeVisible()
  const consoleBefore = await webConsole.boundingBox()
  expect(consoleBefore).not.toBeNull()

  const layoutBefore = await page.evaluate(() => ({
    documentHeight: document.documentElement.scrollHeight,
    viewportHeight: window.innerHeight,
  }))
  expect(layoutBefore.documentHeight).toBeLessThanOrEqual(layoutBefore.viewportHeight + 1)

  const listBefore = await jobList.evaluate((element) => ({
    clientHeight: element.clientHeight,
    scrollHeight: element.scrollHeight,
  }))
  expect(listBefore.scrollHeight).toBeGreaterThan(listBefore.clientHeight)

  await jobList.hover()
  await page.mouse.wheel(0, 600)
  await expect.poll(() => jobList.evaluate((element) => element.scrollTop)).toBeGreaterThan(0)
  expect(await page.evaluate(() => window.scrollY)).toBe(0)

  const consoleAfter = await webConsole.boundingBox()
  expect(consoleAfter).not.toBeNull()
  expect(consoleAfter?.y).toBeCloseTo(consoleBefore?.y || 0, 0)

  await page.setViewportSize({ width: 1024, height: 700 })
  const narrowLayout = await page.evaluate(() => {
    const main = document.querySelector("main")
    return {
      documentHeight: document.documentElement.scrollHeight,
      mainOverflowY: main ? getComputedStyle(main).overflowY : "missing",
      viewportHeight: window.innerHeight,
    }
  })
  expect(narrowLayout.documentHeight).toBeGreaterThan(narrowLayout.viewportHeight)
  expect(narrowLayout.mainOverflowY).not.toBe("hidden")

  await page.waitForTimeout(postRenderObservationMs)
  diagnostics.expectClean()
})

test("reveals Jobs with Runner logs and a GitHub link before secondary metadata", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")

  const diagnostics = observeBrowserDiagnostics(page)
  const runners = fixtureRunners(1)
  let releaseJobs = () => {}
  let releaseGitHubApp = () => {}
  const jobsGate = new Promise<void>((resolve) => { releaseJobs = resolve })
  const githubAppGate = new Promise<void>((resolve) => { releaseGitHubApp = resolve })
  let runnerLogRequests = 0
  let groupRequests = 0
  const githubLogRequests: string[] = []
  page.on("request", (request) => {
    if (new URL(request.url()).pathname.endsWith("/github-log")) githubLogRequests.push(request.url())
  })

  await page.route("**/auth/session", (route) => route.fulfill({
    json: { authenticated: true, oauth_enabled: true, login: "fixture-user", role: "user" },
  }))
  await page.route("**/user/**", async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname === "/user/runner_requests") {
      await jobsGate
      await route.fulfill({ headers: { "X-Total-Count": "1" }, json: runners })
    } else if (url.pathname === "/user/github-app") {
      await githubAppGate
      await route.fulfill({ json: { setup_url: "/github-app/setup", installations: [] } })
    } else if (url.pathname === "/user/onboarding/product-tour") {
      await route.fulfill({ json: { version: 1, status: "completed", tour_seen: true } })
    } else if (url.pathname.startsWith("/user/github/branches/")) {
      groupRequests += 1
      const selected = runners[0]
      await route.fulfill({ json: {
        key: `branch:${selected.repository_full_name}:${selected.head_branch}:${selected.head_sha}`,
        group: "branch", repository: selected.repository_full_name,
        title: selected.head_branch, subtitle: selected.head_sha, updated_at: selected.updated_at,
        jobs: [selected], current_jobs: [selected], previous_jobs: [],
        workflow_run_ids: [selected.workflow_run_id], head_sha: selected.head_sha, head_branch: selected.head_branch,
      } })
    } else if (url.pathname.endsWith("/events")) {
      runnerLogRequests += 1
      await route.fulfill({ json: fixtureRunnerEvents() })
    } else {
      await route.fulfill({ status: 404, body: "fixture route not found" })
    }
  })

  try {
    const response = await page.goto("/jobs", { waitUntil: "domcontentloaded" })
    expect(response?.ok()).toBe(true)
    await expect(page.getByRole("status", { name: /Loading jobs|正在加载任务/ })).toBeVisible()

    releaseJobs()
    await expect(page.getByRole("button", { name: /fixture\/repository-0/ })).toBeVisible()
    await expect(page.getByRole("tab", { name: /Runner logs|Runner 日志/i })).toHaveAttribute("aria-selected", "true")
    await expect(page.getByRole("tab", { name: /GitHub logs|GitHub 日志/i })).toHaveCount(0)
    await expect(page.getByText("fixture Runner log")).toBeVisible()
    const timeline = page.getByRole("region", { name: /Run history|运行记录/, exact: true })
    await expect(timeline.getByRole("tab", { name: /All|全部/, exact: true })).toHaveAttribute("aria-selected", "true")
    await expect(timeline.locator("[data-runner-event-id]")).toHaveCount(3)
    await expect(timeline.getByText("fixture Runner stdout", { exact: true })).toBeVisible()
    await expect(timeline.getByText("fixture Runner stderr", { exact: true })).toBeVisible()
    await expect(timeline.getByText("control · runner_hook", { exact: true })).toBeVisible()
    await timeline.getByRole("tab", { name: "stderr", exact: true }).click()
    await expect(timeline.locator("[data-runner-event-id]")).toHaveCount(1)
    await expect(timeline.getByText("fixture Runner stderr", { exact: true })).toBeVisible()
    await timeline.getByRole("tab", { name: /All|全部/, exact: true }).click()
    await expect(timeline.locator("[data-runner-event-id]")).toHaveCount(3)
    const githubLink = page.locator(`a[href="${runners[0].github_job_url}"]`)
    await expect(githubLink).toHaveCount(1)
    await expect(githubLink).toHaveText(runners[0].assigned_job_name!)
    await expect(githubLink).toHaveAttribute("href", runners[0].github_job_url!)
    await expect(githubLink).toHaveAttribute("target", "_blank")
    await expect(githubLink).toHaveAttribute("title", /View job on GitHub|在 GitHub 查看 Job/)
    const workflowLink = page.getByRole("link", { name: runners[0].workflow_name!, exact: true })
    await expect(workflowLink).toHaveAttribute("href", runners[0].github_job_url!.split("/job/")[0])
    await expect(workflowLink).toHaveAttribute("title", /View workflow run on GitHub|在 GitHub 查看 Workflow Run/)
    await expect.poll(() => groupRequests).toBe(1)
    await page.screenshot({ path: test.info().outputPath("job-github-link.png"), fullPage: true })
    expect(runnerLogRequests).toBe(1)
    await page.getByRole("button", { name: /fixture\/repository-0/ }).click()
    expect(groupRequests).toBe(1)
    expect(githubLogRequests).toEqual([])
    diagnostics.expectClean()
  } finally {
    releaseJobs()
    releaseGitHubApp()
  }
})

test("uses Runner logs and GitHub navigation in the standalone Job fallback", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")
  const diagnostics = observeBrowserDiagnostics(page)
  let selected = fixtureRunners(1)[0]
  const requests: string[] = []
  let jobReads = 0
  await page.route("**/auth/session", (route) => route.fulfill({
    json: { authenticated: true, oauth_enabled: true, login: "fixture-user", role: "user" },
  }))
  await page.route("**/user/**", async (route) => {
    const path = new URL(route.request().url()).pathname
    requests.push(path)
    if (path === `/user/runner_requests/${selected.id}`) {
      jobReads += 1
      if (jobReads >= 3) selected = { ...selected, status: "completed" }
      await route.fulfill({ json: selected })
    } else if (path.endsWith("/group")) {
      await route.fulfill({ json: {
        key: "", group: "manual", repository: selected.repository_full_name,
        jobs: [selected], current_jobs: [selected], previous_jobs: [], workflow_run_ids: [],
      } })
    } else if (path.endsWith("/events")) {
      const eventPage = fixtureRunnerEvents()
      if (selected.status === "completed") eventPage.events.push({
        id: 4, event_type: "control_log", stage: "runner_cleanup", message: "fixture final cleanup", created_at: "2026-09-29T03:51:03.999Z",
      })
      await route.fulfill({ json: eventPage })
    } else if (path === "/user/github-app") {
      await route.fulfill({ json: { setup_url: "/github-app/setup", installations: [] } })
    } else if (path === "/user/onboarding/product-tour") {
      await route.fulfill({ json: { version: 1, status: "completed", tour_seen: true } })
    } else {
      await route.fulfill({ status: 404, body: "fixture route not found" })
    }
  })
  const original = selected
  for (const variant of ["job", "run", "none"]) {
    selected = { ...original,
      github_job_url: variant === "job" ? original.github_job_url : undefined,
      workflow_run_id: variant === "none" ? undefined : original.workflow_run_id,
    }
    await page.goto(`/jobs/${selected.id}`, { waitUntil: "networkidle" })
    await expect(page.getByRole("tab", { name: /Runner logs|Runner 日志/i })).toHaveAttribute("aria-selected", "true")
    await expect(page.getByText("fixture Runner log")).toBeVisible()
    await expect(page.getByRole("tab", { name: /GitHub logs|GitHub 日志/i })).toHaveCount(0)
    const link = page.getByRole("link", { name: /View job on GitHub|View workflow run on GitHub|在 GitHub 查看/ })
    if (variant === "none") {
      await expect(link).toHaveCount(0)
    } else {
      await expect(link).toHaveAttribute("href", variant === "job" ? original.github_job_url! : "https://github.com/fixture/repository-0/actions/runs/20000")
      await expect(link).toHaveAttribute("target", "_blank")
    }
    if (variant === "job") {
      await expect(page.locator("header").getByText(/Completed|已完成/, { exact: true })).toBeVisible({ timeout: 15000 })
      await expect(page.getByText("fixture final cleanup", { exact: true })).toBeVisible()
      await expect(page.locator("[data-runner-event-id]")).toHaveCount(4)
    }
    diagnostics.expectClean()
  }
  expect(requests.filter((path) => path.endsWith("/github-log"))).toEqual([])
})

test("reloads Jobs after returning from another page while an older list request is pending", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")

  const runners = fixtureRunners(1)
  let releaseFirstJobs = () => {}
  const firstJobsGate = new Promise<void>((resolve) => { releaseFirstJobs = resolve })
  let jobsRequests = 0

  await page.route("**/auth/session", (route) => route.fulfill({
    json: { authenticated: true, oauth_enabled: true, login: "fixture-user", role: "user" },
  }))
  await page.route("**/user/**", async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname === "/user/runner_requests") {
      jobsRequests += 1
      if (jobsRequests === 1) await firstJobsGate
      await route.fulfill({ headers: { "X-Total-Count": "1" }, json: runners })
    } else if (url.pathname === "/user/github-app") {
      await route.fulfill({ json: { setup_url: "/github-app/setup", installations: [] } })
    } else if (url.pathname === "/user/runner-specs") {
      await route.fulfill({ json: { items: [], sandbox_source: "none" } })
    } else if (url.pathname === "/user/onboarding/product-tour") {
      await route.fulfill({ json: { version: 1, status: "completed", tour_seen: true } })
    } else if (url.pathname.endsWith("/events")) {
      await route.fulfill({ json: fixtureRunnerEvents() })
    } else {
      await route.fulfill({ status: 404, body: "fixture route not found" })
    }
  })

  try {
    await page.goto("/jobs", { waitUntil: "domcontentloaded" })
    await expect(page.getByRole("status", { name: /Loading jobs|正在加载任务/ })).toBeVisible()
    await expect.poll(() => jobsRequests).toBe(1)
    await page.locator('nav a[href="/runner-specs"]').first().click()
    await page.locator('nav a[href="/jobs"]').first().click()
    await expect(page.getByRole("button", { name: /fixture\/repository-0/ })).toBeVisible({ timeout: 3_000 })
  } finally {
    releaseFirstJobs()
  }
})

test("deploys a historical PR Job through the repository context", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")

  const diagnostics = observeBrowserDiagnostics(page)
  const pageLoads = observePageLoads(page)
  let submitted: Record<string, unknown> = {}
  const runners = fixtureRunners(100)
  const selected = { ...runners[0], pull_request_number: 42 }
  runners[0] = selected
  const previous: RunnerState = {
    ...selected,
    id: "fixture-previous-job",
    status: "completed",
    workflow_job_id: 30_000,
    workflow_run_id: 40_000,
    workflow_name: "Historical workflow",
    head_sha: "f".repeat(40),
    created_at: "2026-08-11T00:00:00Z",
    updated_at: "2026-08-11T00:00:00Z",
    completed_at: "2026-08-11T00:00:00Z",
  }

  const deployment = { id: "historical-pr", repository_full_name: selected.repository_full_name, source: "pull_request", ref: "42", pull_request_number: 42, commit_sha: previous.head_sha, revision_number: 1, status: "queued", created_at: previous.created_at, expires_at: previous.created_at }
  await page.route("**/auth/session", (route) => route.fulfill({
    json: { authenticated: true, oauth_enabled: true, login: "fixture-user", role: "user" },
  }))
  await page.route("**/user/**", async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname === "/user/runner_requests") {
      await route.fulfill({ headers: { "X-Total-Count": "101" }, json: runners })
    } else if (url.pathname === "/user/github-app") {
      await route.fulfill({ json: { setup_url: "/github-app/setup", installations: [{ id: 1, account_login: "fixture", account_type: "Organization", manageable: true, repositories: [selected.repository_full_name] }] } })
    } else if (url.pathname === "/user/onboarding/product-tour") {
      await route.fulfill({ json: { version: 1, status: "completed", tour_seen: true } })
    } else if (url.pathname.startsWith("/user/github/pulls/")) {
      await route.fulfill({ json: {
        key: `pr:${selected.repository_full_name}:42`,
        group: "pull_request", repository: selected.repository_full_name,
        title: "PR #42", subtitle: selected.head_branch, updated_at: selected.updated_at,
        jobs: [selected, previous], current_jobs: [selected], previous_jobs: [previous],
        workflow_run_ids: [selected.workflow_run_id, previous.workflow_run_id],
        head_sha: selected.head_sha, head_branch: selected.head_branch,
        pull_request_number: 42,
      } })
    } else if (url.pathname.endsWith("/repositories")) {
      await route.fulfill({ json: { repositories: [selected.repository_full_name] } })
    } else if (url.pathname === "/user/deployment-refs") {
      await route.fulfill({ json: { refs: [], has_more: false } })
    } else if (url.pathname === "/user/deployment-templates") {
      await route.fulfill({ json: { templates: [{ template_id: "base", names: ["base"] }] } })
    } else if (url.pathname === "/user/deployments" && route.request().method() === "POST") {
      submitted = route.request().postDataJSON()
      await route.fulfill({ status: 201, json: deployment })
    } else if (url.pathname === "/user/deployments/historical-pr") {
      await route.fulfill({ json: deployment })
    } else if (url.pathname.endsWith("/resources")) {
      await route.fulfill({ json: {} })
    } else if (url.pathname.endsWith("/events")) {
      await route.fulfill({ json: fixtureRunnerEvents() })
    } else {
      await route.fulfill({ status: 404, body: "fixture route not found" })
    }
  })

  await page.goto("/jobs", { waitUntil: "domcontentloaded" })
  await expect(page.getByRole("button", { name: /fixture\/repository-0/ })).toBeVisible()
  await expect(page.getByRole("button", { name: "Historical workflow" })).toBeVisible()
  await page.getByRole("button", { name: "Historical workflow" }).click()
  await expect(page).toHaveURL(/\/github\/fixture\/repository-0\/pull_requests\/42\/jobs\?job=fixture-previous-job$/)
  await page.reload({ waitUntil: "networkidle" })
  await expect(page.getByRole("link", { name: "Deployment history", exact: true })).toHaveAttribute("href", "/github/fixture/repository-0/pull_requests/42/deployments")
  const jobLoads = pageLoads()
  await page.getByRole("link", { name: "Deploy this commit", exact: true }).click()
  await expect(page.locator("#deployment-installation")).toHaveValue("1")
  await expect(page.locator("#deployment-source")).toHaveValue("pull_request")
  await expect(page.locator("#deployment-ref")).toHaveValue("42")
  await expect(page.getByText(previous.head_sha!, { exact: true })).toBeVisible()
  expect(pageLoads()).toEqual(jobLoads)
  await page.reload({ waitUntil: "networkidle" })
  await expect(page.locator("#deployment-ref")).toHaveValue("42")
  await page.locator('button[type="submit"]').click()
  expect(submitted).toMatchObject({ repository_full_name: selected.repository_full_name, source: "pull_request", ref: "42", commit_sha: previous.head_sha })
  await expect(page).toHaveURL(/\/github\/fixture\/repository-0\/pull_requests\/42\/deployments\/historical-pr$/)
  await page.reload({ waitUntil: "networkidle" })
  await expect(page.getByRole("heading", { name: "Deployment", exact: true })).toBeVisible()
  diagnostics.expectClean()
})

test("does not show the previous account preferences during an in-place session change", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")

  let sessionChecks = 0
  let bobPreferencesStarted = false
  let releaseBobPreferences = () => {}
  const bobPreferencesGate = new Promise<void>((resolve) => { releaseBobPreferences = resolve })
  const preferences = (bucket: string) => ({
    cache: { configured: true, region: "fixture-region", endpoint: "https://fixture.example", bucket, prefix: "fixture/" },
    sandbox: { mode: "custom", resolved_source: "none", api_url: "", api_key: { configured: false } },
  })

  await page.route("**/auth/session", (route) => route.fulfill({
    json: { authenticated: true, oauth_enabled: true, login: ++sessionChecks === 1 ? "alice" : "bob", role: "user" },
  }))
  await page.route("**/sandbox/regions", (route) => route.fulfill({ json: [] }))
  await page.route("**/user/**", async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname === "/user/github-app") {
      await route.fulfill({ json: { setup_url: "/github-app/setup", settings_manageability: true, installations: [] } })
    } else if (url.pathname === "/user/preferences/cache" && route.request().method() === "DELETE") {
      await route.fulfill({ status: 401 })
    } else if (url.pathname === "/user/preferences") {
      if (sessionChecks > 1) {
        bobPreferencesStarted = true
        await bobPreferencesGate
      }
      await route.fulfill({ json: preferences(sessionChecks > 1 ? "bob-bucket" : "alice-bucket") })
    } else if (url.pathname === "/user/onboarding/product-tour") {
      await route.fulfill({ json: { version: 1, status: "completed", tour_seen: true } })

    } else {
      await route.fulfill({ status: 404, body: "fixture route not found" })
    }
  })

  try {
    await page.goto("/account/preferences", { waitUntil: "domcontentloaded" })
    await expect(page.locator("#cache-bucket")).toHaveValue("alice-bucket")
    await page.locator("form").filter({ has: page.locator("#cache-bucket") }).getByRole("button", { name: "Remove" }).click()
    await expect.poll(() => bobPreferencesStarted).toBe(true)
    await expect(page.locator("#cache-bucket")).toHaveValue("")
  } finally {
    releaseBobPreferences()
  }
})

test("reports a preferences failure without treating GitHub accounts as failed", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")

  await page.route("**/auth/session", (route) => route.fulfill({
    json: { authenticated: true, oauth_enabled: true, login: "alice", role: "user" },
  }))
  await page.route("**/sandbox/regions", (route) => route.fulfill({ json: [] }))
  await page.route("**/user/**", async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname === "/user/github-app") {
      await route.fulfill({ json: { setup_url: "/github-app/setup", settings_manageability: true, installations: [] } })
    } else if (url.pathname === "/user/preferences") {
      await route.fulfill({ status: 500, body: "" })
    } else if (url.pathname === "/user/onboarding/product-tour") {
      await route.fulfill({ json: { version: 1, status: "completed", tour_seen: true } })

    } else {
      await route.fulfill({ status: 404, body: "fixture route not found" })
    }
  })

  await page.goto("/account/preferences", { waitUntil: "domcontentloaded" })
  await expect(page.getByText("Could not load preferences. Try again.")).toBeVisible()
  await expect(page.getByRole("heading", { name: "alice", exact: true })).toBeVisible()
})

test("filters the platform spec directory to enabled published public entries", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")
  const diagnostics = observeBrowserDiagnostics(page)
  await page.route("**/auth/session", (route) => route.fulfill({ json: { authenticated: true, oauth_enabled: true, login: "fixture-user", role: "user" } }))
  await page.route("**/user/**", (route) => {
    if (new URL(route.request().url()).pathname === "/user/runner-specs") {
      const base = { workflow_labels: ["qiniu", "ubuntu"], enabled: true, max_concurrency: 4, overrides_global: false, updated_at: "2026-10-01T00:00:00Z" }
      return route.fulfill({ json: { items: [
        { ...base, name: "visible-public", source: "platform_public", published: true, default_template_name: "public-template" },
        { ...base, name: "unpublished-public", source: "platform_public", published: false },
        { ...base, name: "disabled-public", source: "platform_public", published: true, enabled: false },
        { ...base, name: "public-fixed-id", source: "platform_custom", published: true },
        { ...base, name: "private-business", source: "platform_custom", published: false },
        { ...base, name: "scope-custom", source: "scoped_custom", template_id: "scope-id" },
      ] } })
    }
    return route.fulfill({ json: { version: 1, status: "completed", tour_seen: true } })
  })
  await page.route("**/sandbox/regions", (route) => route.fulfill({ json: [] }))
  await page.goto("/runner-specs", { waitUntil: "networkidle" })
  await expect(page.getByText("visible-public", { exact: true })).toBeVisible()
  await expect(page.getByText("public-fixed-id", { exact: true })).toBeVisible()
  for (const name of ["unpublished-public", "disabled-public", "private-business", "scope-custom", "private-id", "scope-id"]) {
    await expect(page.getByText(name, { exact: true })).toHaveCount(0)
  }
  diagnostics.expectClean()
})

test("checks template visibility independently of reference type and preserves runtime bindings", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")
  const diagnostics = observeBrowserDiagnostics(page)
  let profile = { name: "old-managed", labels: ["qiniu", "ubuntu"], required_labels: ["qiniu", "ubuntu"], template_source: "public", default_template_name: "public-template", template_id: "", published: true, managed_by: "qiniu/ci-runner", enabled: true, max_concurrency: 7, min_idle: 0, priority: 1, runner_group: "", updated_at: "2026-10-01T00:00:00Z" }
  let saved: Record<string, unknown> | undefined
  await page.route("**/auth/session", (route) => route.fulfill({ json: { authenticated: true, oauth_enabled: true, login: "fixture-admin", role: "admin" } }))
  await page.route("**/runner_specs**", (route) => {
    const url = new URL(route.request().url())
    if (url.pathname.startsWith("/admin/")) return route.continue()
    if (url.pathname === "/runner_specs/templates/status") {
      const template = url.searchParams.get("template") || ""
      if (template === "unavailable") return route.fulfill({ contentType: "application/json", body: "unavailable" })
      return route.fulfill({ json: { template: { template_id: template === "public-fixed-id" ? "public-fixed-id" : "resolved-id", names: ["team/ubuntu-24.04"], cpu_count: 8, memory_mb: 16384, disk_size_mb: 81920, envd_version: "0.5.1", build_status: "failed", created_at: "2026-01-02T03:04:05Z", updated_at: "2026-10-09T06:00:00Z" }, public: template !== "private-template", runnable: template !== "no-default-build", template_source: template === "public-fixed-id" || template === "private-template" ? "private" : "public", template_id: template === "public-fixed-id" || template === "private-template" ? template : "", default_template_name: template === "public-fixed-id" || template === "private-template" ? "" : template } })
    }
    if (route.request().method() === "PATCH") {
      saved = route.request().postDataJSON()
      profile = { ...profile, ...saved }
      return route.fulfill({ json: profile })
    }
    return route.fulfill({ json: [profile] })
  })
  await page.goto("/admin/runner_specs", { waitUntil: "networkidle" })
  await page.setViewportSize({ width: 1485, height: 1000 })
  await page.getByRole("button", { name: "Edit old-managed", exact: true }).click()
  await expect(page.locator("#runner-spec-labels")).toBeEnabled()
  await expect(page.locator("#runner-spec-template")).toBeEnabled()
  await expect(page.locator("#runner-spec-template-source")).toHaveCount(0)
  await expect(page.locator("#runner-spec-published")).toBeEnabled()
  await expect(page.locator("#runner-spec-update-policy, #runner-spec-require_docker, #runner-spec-fork_sponsorship")).toHaveCount(0)
  const longLabels = `self-hosted,linux,x64,qiniu,ubuntu-24.04,${"long-label-".repeat(12)}`
  const longTemplate = `github-runner-ubuntu-24-04-${"extended-".repeat(12)}`
  await page.locator("#runner-spec-labels").fill(longLabels)
  await page.locator("#runner-spec-template").fill(longTemplate)
  const assertWrappedFields = async () => {
    await expect.poll(() => page.locator("#runner-spec-labels, #runner-spec-template").evaluateAll((fields) => fields.every((field) => field.scrollWidth <= field.clientWidth + 1 && field.scrollHeight <= field.clientHeight + 1))).toBe(true)
    expect(await page.getByRole("dialog").evaluate((dialog) => dialog.scrollWidth <= dialog.clientWidth + 1)).toBe(true)
  }
  await assertWrappedFields()
  expect(await page.getByRole("dialog").evaluate((dialog) => dialog.getBoundingClientRect().width)).toBeGreaterThan(700)
  await page.setViewportSize({ width: 390, height: 844 })
  await assertWrappedFields()
  await expect(page.locator("#runner-spec-labels")).toHaveValue(longLabels)
  await expect(page.locator("#runner-spec-template")).toHaveValue(longTemplate)
  await page.locator("#runner-spec-labels").fill("qiniu,ubuntu,new-label")
  await page.locator("#runner-spec-template").fill("private-template")
  await expect(page.locator("#runner-spec-published")).not.toBeChecked()
  await expect(page.locator("#runner-spec-published")).toBeDisabled()
  await expect(page.getByRole("dialog").getByText("This template is not public and cannot be shown in the catalog.")).toBeVisible()
  await page.locator("#runner-spec-template").fill("unavailable")
  await expect(page.getByRole("dialog").getByText(/Cannot verify template visibility/)).toBeVisible()
  await expect(page.getByRole("dialog").getByLabel("Sandbox template details")).toHaveCount(0)
  await expect(page.locator("#runner-spec-published")).toBeDisabled()
  await page.locator("#runner-spec-template").fill("no-default-build")
  await expect(page.getByRole("dialog").getByText(/No usable default build is available yet/)).toBeVisible()
  await expect(page.locator("#runner-spec-published")).toBeEnabled()
  await page.locator("#runner-spec-published").check()
  await page.locator("#runner-spec-template").fill("public-fixed-id")
  await expect(page.locator("#runner-spec-published")).toBeEnabled()
  await expect(page.getByRole("dialog").getByText("The latest template build failed, but a usable default build is still available.")).toBeVisible()
  await expect(page.getByRole("dialog").getByText(/No usable default build is available yet/)).toHaveCount(0)
  const details = page.getByRole("dialog").getByRole("definition").filter({ hasText: "0.5.1" })
  await expect(details).toBeVisible()
  await expect(page.getByRole("dialog").getByLabel("Sandbox template details")).toContainText("team/ubuntu-24.04")
  await expect(page.getByRole("dialog").getByLabel("Sandbox template details")).toContainText("8 vCPU · 16384 MB RAM · 81920 MB disk")
  await expect(page.getByRole("dialog").getByLabel("Sandbox template details")).toContainText("public-fixed-id")
  await assertWrappedFields()
  for (const label of ["Template name", "Template ID", "Configuration", "ENVD version", "Public template", "Created at", "Updated at"]) {
    await expect(page.getByRole("dialog").getByLabel("Sandbox template details").getByText(label, { exact: true })).toHaveCount(1)
  }
  await page.setViewportSize({ width: 1485, height: 1566 })
  await page.screenshot({ path: "test-results/admin-template-details.png", fullPage: true })
  await page.locator("#runner-spec-published").check()
  await page.getByRole("button", { name: "Save runner spec", exact: true }).click()
  await expect(page.getByRole("dialog")).toHaveCount(0)
  expect(saved).toMatchObject({ template_source: "private", template_id: "public-fixed-id", default_template_name: "", published: true, expected_updated_at: "2026-10-01T00:00:00Z", labels: ["qiniu", "ubuntu", "new-label"] })
  await expect(page.getByRole("article", { name: "old-managed", exact: true })).toContainText("public-fixed-id")
  diagnostics.expectClean()
})

test("explains missing admin template credentials and retries name and ID details", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")
  const pageErrors: string[] = []
  page.on("pageerror", (error) => pageErrors.push(error.message))
  const profile = { name: "existing-spec", labels: ["qiniu", "ubuntu"], required_labels: ["qiniu", "ubuntu"], template_source: "public", default_template_name: "public-template", template_id: "", published: true, enabled: true, max_concurrency: 7, min_idle: 0, priority: 1, runner_group: "" }
  let configured = false
  const lookups: string[] = []
  await page.route("**/auth/session", (route) => route.fulfill({ json: { authenticated: true, oauth_enabled: true, login: "fixture-admin", role: "admin" } }))
  await page.route("**/runner_specs**", (route) => {
    const url = new URL(route.request().url())
    if (url.pathname.startsWith("/admin/")) return route.continue()
    if (url.pathname === "/runner_specs/templates/status") {
      lookups.push(url.search)
      if (!configured) return route.fulfill({ status: 409, json: { code: "sandbox_service_not_configured", error: "Configure the admin Sandbox service" } })
      return route.fulfill({ json: { public: true, runnable: true, template: { template_id: "physical-id", names: ["public-template"], cpu_count: 4, memory_mb: 8192, disk_size_mb: 20480, envd_version: "0.5.1", created_at: "2026-01-02T03:04:05Z", updated_at: "2026-10-09T06:00:00Z" } } })
    }
    return route.fulfill({ json: [profile] })
  })
  await page.goto("/admin/runner_specs", { waitUntil: "networkidle" })
  await page.getByRole("button", { name: "Edit existing-spec", exact: true }).click()
  await expect(page.getByRole("dialog").getByText(/The admin Sandbox service has no endpoint or API key configured/)).toBeVisible()
  await expect(page.getByRole("dialog").getByRole("link", { name: "Configure Sandbox service" })).toHaveAttribute("href", "/admin/sandbox_service")
  await expect(page.getByRole("dialog").getByLabel("Sandbox template details")).toHaveCount(0)
  await expect(page.locator("#runner-spec-published")).toBeChecked()
  const previousLookups = lookups.length
  configured = true
  await page.getByRole("dialog").getByRole("button", { name: "Retry lookup" }).click()
  const details = page.getByRole("dialog").getByLabel("Sandbox template details")
  await expect(details).toContainText("physical-id")
  await expect(details).toContainText("4 vCPU · 8192 MB RAM · 20480 MB disk")
  await expect(page.getByRole("dialog").getByRole("button", { name: "Retry lookup" })).toHaveCount(0)
  expect(lookups).toHaveLength(previousLookups + 1)
  expect(lookups.at(-1)).toBe(lookups[0])
  expect(lookups[0]).toContain("reference_type=name")
  await page.locator("#runner-spec-template").fill("physical-id")
  await expect.poll(() => lookups.length).toBe(previousLookups + 2)
  await expect(details).toContainText("public-template")
  await expect(page.locator("#runner-spec-published")).toBeEnabled()
  expect(pageErrors).toEqual([])
})

test("shows complete admin spec cards while template lookups load or fail", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  const longLabel = "extra-label-".repeat(20)
  const base = { labels: ["self-hosted", "qiniu", "ubuntu", longLabel], required_labels: ["qiniu", "ubuntu"], template_source: "public", default_template_name: "shared-template", template_id: "", published: true, enabled: true, max_concurrency: 7, min_idle: 2, priority: 100, runner_group: "organization-runners" }
  const profiles = [{ ...base, name: "standard" }, { ...base, name: "disabled", enabled: false }, { ...base, name: "private", published: false, template_source: "private", default_template_name: "", template_id: "private-id" }]
  let releasePublic!: () => void
  const publicReady = new Promise<void>((resolve) => { releasePublic = resolve })
  let publicLookups = 0
  let privateRecovered = false
  await page.route("**/auth/session", (route) => route.fulfill({ json: { authenticated: true, oauth_enabled: true, login: "fixture-admin", role: "admin" } }))
  await page.route("**/runner_specs**", async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname.startsWith("/admin/")) return route.continue()
    if (url.pathname === "/runner_specs/templates/status") {
      const isPrivate = url.searchParams.get("template") === "private-id"
      if (isPrivate && !privateRecovered) return route.fulfill({ status: 504, json: { code: "template_validation_timeout", error: "Template lookup timed out" } })
      if (!isPrivate) { publicLookups++; await publicReady }
      return route.fulfill({ json: { public: !isPrivate, runnable: true, template: { template_id: isPrivate ? "private-id" : "shared-id", names: [isPrivate ? "team/private" : "team/shared-template"], cpu_count: 8, memory_mb: 8192, disk_size_mb: 20480, envd_version: "0.5.6", created_at: "2026-08-03T09:18:03Z", updated_at: "2026-09-21T08:59:00Z" } } })
    }
    return route.fulfill({ json: profiles })
  })
  await page.goto("/admin/runner_specs", { waitUntil: "domcontentloaded" })
  const standard = page.getByRole("article", { name: "standard", exact: true })
  const disabled = page.getByRole("article", { name: "disabled", exact: true })
  const privateSpec = page.getByRole("article", { name: "private", exact: true })
  await expect(page.getByRole("article")).toHaveCount(3)
  await expect(page.getByRole("table")).toHaveCount(0)
  await expect(standard).toContainText(longLabel)
  await expect(standard).toContainText("Required labels")
  await expect(standard).toContainText("Max concurrency7")
  await expect(standard).toContainText("Min idle2")
  await expect(standard).toContainText("Priority100")
  await expect(standard).toContainText("organization-runners")
  await expect(disabled.getByText("Disabled", { exact: true })).toBeVisible()
  await expect(standard.getByText(/Checking template visibility/)).toBeVisible()
  await expect(privateSpec.getByText(/template lookup timed out/)).toBeVisible()
  await expect.poll(() => publicLookups).toBe(1)
  await standard.getByRole("button", { name: "Edit standard", exact: true }).click()
  releasePublic()
  await expect(page.getByRole("dialog").getByLabel("Sandbox template details")).toContainText("shared-id")
  expect(publicLookups).toBe(1)
  await page.getByRole("dialog").getByRole("button", { name: "Cancel", exact: true }).click()
  await expect(standard.getByLabel("Sandbox template details")).toContainText("0.5.6")
  privateRecovered = true
  await privateSpec.getByRole("button", { name: "Retry lookup", exact: true }).click()
  await expect(privateSpec.getByLabel("Sandbox template details")).toContainText("team/private")
  await expect(privateSpec).toContainText("Catalog display: Off")
  await page.getByRole("button", { name: "Refresh", exact: true }).click()
  await expect.poll(() => publicLookups).toBe(2)
  await expect(standard.getByLabel("Sandbox template details")).toContainText("shared-id")
  for (const width of [961, 390]) {
    await page.setViewportSize({ width, height: 1034 })
    expect(await page.locator("body").evaluate((body) => body.scrollWidth <= window.innerWidth)).toBe(true)
    expect(await page.getByRole("article").evaluateAll((cards) => cards.every((card) => card.scrollWidth <= card.clientWidth))).toBe(true)
  }
  await page.setViewportSize({ width: 961, height: 1034 })
  await page.screenshot({ path: "test-results/admin-spec-cards.png", fullPage: true })
  expect(errors).toEqual([])
})

test("requires confirmation before deleting an admin Runner Spec", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "Local fixture-backed admin coverage")
  const diagnostics = observeBrowserDiagnostics(page)
  const base = { labels: ["qiniu", "ubuntu"], required_labels: ["qiniu", "ubuntu"], template_source: "public", default_template_name: "public-template", template_id: "", published: true, enabled: true, max_concurrency: 7, min_idle: 0, priority: 1, runner_group: "", updated_at: "2026-10-01T00:00:00Z" }
  let profiles = [{ ...base, name: "delete-me" }, { ...base, name: "keep-me" }]
  const deletions: string[] = []
  await page.route("**/auth/session", (route) => route.fulfill({ json: { authenticated: true, oauth_enabled: true, login: "fixture-admin", role: "admin" } }))
  await page.route("**/runner_specs**", (route) => {
    const url = new URL(route.request().url())
    if (url.pathname.startsWith("/admin/")) return route.continue()
    if (route.request().method() === "DELETE") {
      deletions.push(url.pathname)
      profiles = profiles.filter((profile) => profile.name !== "delete-me")
      return route.fulfill({ json: {} })
    }
    return route.fulfill({ json: profiles })
  })
  await page.goto("/admin/runner_specs", { waitUntil: "networkidle" })
  const remove = page.getByRole("button", { name: "Delete delete-me", exact: true })
  await expect(remove).toHaveText("")
  await expect(page.getByRole("button", { name: "Edit delete-me", exact: true })).toHaveText("")
  await remove.click()
  const confirmation = page.getByRole("dialog", { name: "Delete Runner Spec", exact: true })
  await expect(confirmation).toContainText('Delete Runner Spec "delete-me"?')
  expect(deletions).toEqual([])
  await confirmation.getByRole("button", { name: "Cancel", exact: true }).click()
  await expect(confirmation).toHaveCount(0)
  expect(deletions).toEqual([])
  await remove.click()
  await page.keyboard.press("Escape")
  await expect(confirmation).toHaveCount(0)
  expect(deletions).toEqual([])
  await remove.click()
  await confirmation.getByRole("button", { name: "Delete", exact: true }).click()
  await expect(remove).toHaveCount(0)
  await expect(confirmation).toHaveCount(0)
  expect(deletions).toEqual(["/runner_specs/delete-me"])
  await expect(page.getByRole("button", { name: "Delete keep-me", exact: true })).toBeVisible()
  diagnostics.expectClean()
})

test("selects an organization Runner group and saves matching workflow YAML", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")
  const diagnostics = observeBrowserDiagnostics(page)
  let groupReads = 0
  let saved: Record<string, unknown> | undefined
  let items: Record<string, unknown>[] = []
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"])
  await page.setViewportSize({ width: 390, height: 844 })
  await page.route("**/auth/session", (route) => route.fulfill({ json: { authenticated: true, oauth_enabled: true, login: "fixture-owner", role: "user" } }))
  await page.route("**/sandbox/regions", (route) => route.fulfill({ json: [] }))
  await page.route("**/user/**", async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname === "/user/github-app") {
      return route.fulfill({ json: { setup_url: "/github-app/setup", settings_manageability: true, installations: [{ id: 1, account_id: 1, installation_id: 987, account_login: "fixture-org", account_type: "organization", manageable: true, repositories: ["fixture-org/repo"], created_at: "2026-10-01T00:00:00Z", updated_at: "2026-10-01T00:00:00Z" }] } })
    }
    if (url.pathname === "/user/preferences") return route.fulfill({ json: { cache: { configured: false }, sandbox: { mode: "custom", resolved_source: "custom", api_url: "", manageable: true, api_key: { configured: true } } } })
    if (url.pathname === "/user/onboarding/product-tour") return route.fulfill({ json: { version: 1, status: "completed", tour_seen: true } })
    if (url.pathname === "/user/sandbox/templates") return route.fulfill({ json: [{ template_id: "gpu-template", aliases: ["GPU builder"] }] })
    if (url.pathname === "/user/github-runner-groups") {
      expect(url.searchParams.get("installation_id")).toBe("1")
      groupReads++
      return route.fulfill({ json: { organization: "fixture-org", groups: [{ id: 1, name: "Build", restricted_to_workflows: true }] } })
    }
    if (url.pathname === "/user/runner-specs") {
      if (route.request().method() === "POST") {
        saved = route.request().postDataJSON()
        items = [{ ...saved, source: "scoped_custom", updated_at: "2026-10-12T00:00:00Z" }]
      }
      return route.fulfill({ json: { scope_type: "github_installation", scope_id: 987, sandbox_source: "github_installation", items } })
    }
    return route.fulfill({ status: 404, body: "fixture route not found" })
  })
  await page.goto("/organizations/fixture-org/runner-specs", { waitUntil: "networkidle" })
  await page.getByRole("button", { name: "Create custom runner spec", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await page.locator("#user-runner-name").fill("gpu")
  await page.locator("#user-runner-labels").fill("qiniu, gpu")
  await page.locator("#user-runner-template").selectOption("gpu-template")
  expect(groupReads).toBe(0)
  await expect(dialog).toContainText("Repository that triggered the job")
  await dialog.getByRole("checkbox", { name: "Use an organization Runner group" }).check()
  await page.locator("#user-runner-group").selectOption("Build")
  await expect(dialog).toContainText('group: "Build"')
  await expect(dialog).toContainText('labels: ["qiniu", "gpu"]')
  await expect(dialog).toContainText("Organization fixture-org · group Build")
  await expect(dialog.getByRole("link", { name: "Manage groups on GitHub" })).toHaveAttribute("href", "https://github.com/organizations/fixture-org/settings/actions/runner-groups")
  expect(await dialog.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true)
  await page.screenshot({ path: "test-results/organization-runner-group.png", fullPage: true })
  await dialog.getByRole("button", { name: "Create custom runner spec", exact: true }).click()
  await expect(dialog).toHaveCount(0)
  expect(saved).toMatchObject({ runner_group: "Build", workflow_labels: ["qiniu", "gpu"] })
  await page.getByRole("button", { name: "Copy runs-on YAML" }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('runs-on:\n  group: "Build"\n  labels: ["qiniu", "gpu"]')
  await page.goto("/account/runner-specs", { waitUntil: "networkidle" })
  await page.getByRole("button", { name: "Create custom runner spec", exact: true }).click()
  await expect(page.getByRole("checkbox", { name: "Use an organization Runner group" })).toHaveCount(0)
  await expect(page.getByRole("dialog")).toContainText("Repository that triggered the job")
  diagnostics.expectClean()
})

async function routeLocalAnonymousSession(page: Page) {
  const authSessionRoute = getLocalAuthSessionRoute(process.env.RUNNERD_UI_SMOKE_BASE_URL)
  if (!authSessionRoute) return

  await page.route(authSessionRoute.pattern, async (route) => {
    await route.fulfill({ json: authSessionRoute.json })
  })
}

function observeBrowserDiagnostics(page: Page) {
  const consoleErrors: string[] = []
  const pageErrors: string[] = []
  const failedAssets: string[] = []

  page.on("console", (message) => {
    if (message.type() === "error") {
      consoleErrors.push(message.text())
    }
  })
  page.on("pageerror", (error) => {
    pageErrors.push(error.message)
  })
  page.on("requestfailed", (request) => {
    if (["script", "stylesheet"].includes(request.resourceType())) {
      failedAssets.push(`${request.method()} ${request.url()}: ${request.failure()?.errorText ?? "failed"}`)
    }
  })
  page.on("response", (response) => {
    if (
      response.status() >= 400 &&
      ["script", "stylesheet"].includes(response.request().resourceType())
    ) {
      failedAssets.push(`${response.status()} ${response.url()}`)
    }
  })

  return {
    expectClean() {
      expect(pageErrors, "page errors").toEqual([])
      expect(consoleErrors, "console errors").toEqual([])
      expect(failedAssets, "failed script or stylesheet requests").toEqual([])
    },
  }
}

function observePageLoads(page: Page) {
  let documents = 0
  let sessions = 0
  page.on("request", (request) => {
    if (request.isNavigationRequest() && request.frame() === page.mainFrame()) documents += 1
    if (new URL(request.url()).pathname === "/auth/session") sessions += 1
  })
  return () => ({ documents, sessions })
}

function fixtureRunners(count: number): RunnerState[] {
  return Array.from({ length: count }, (_, index) => {
    const createdAt = new Date(Date.UTC(2026, 7, 12, 0, 0, count - index)).toISOString()
    const id = `fixture-job-${index}`
    return {
      id,
      status: index === 0 ? "running" : "completed",
      repository_full_name: `fixture/repository-${index}`,
      requested_labels: ["self-hosted", "e2b"],
      runner_spec_name: "fixture-spec",
      runner_name: `fixture-runner-${index}`,
      sandbox_id: index === 0 ? "fixture-sandbox" : undefined,
      workflow_job_id: 10_000 + index,
      workflow_run_id: 20_000 + index,
      workflow_name: "Fixture workflow",
      head_branch: `fixture-branch-${index}`,
      head_sha: index.toString(16).padStart(40, "0"),
      assigned_job_name: `Fixture job ${index}`,
      github_job_url: `https://github.com/fixture/repository-${index}/actions/runs/${20_000 + index}/job/${10_000 + index}`,
      created_at: createdAt,
      updated_at: createdAt,
      running_at: index === 0 ? createdAt : undefined,
      completed_at: index === 0 ? undefined : createdAt,
    }
  })
}

function fixtureRunnerEvents() {
  return {
    events: [
      { id: 1, event_type: "control_log", stage: "runner_hook", message: "fixture Runner log", created_at: "2026-09-29T03:51:00.123Z" },
      { id: 2, event_type: "stdout_log", message: "fixture Runner stdout", created_at: "2026-09-29T03:51:01.456Z" },
      { id: 3, event_type: "stderr_log", message: "fixture Runner stderr", created_at: "2026-09-29T03:51:02.789Z" },
    ],
    has_more: false,
  }
}

test("separates deployment list, creation, and refreshable detail pages", async ({ page }, testInfo) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")
  const diagnostics = observeBrowserDiagnostics(page)
  const pageLoads = observePageLoads(page)
  const terminalInputs: string[] = []
  let terminalClosed = false
  await page.addInitScript(() => {
    const BrowserEventSource = window.EventSource
    window.EventSource = class extends BrowserEventSource {
      constructor(url: string | URL, options?: EventSourceInit) {
        if (String(url).includes("/terminal/")) {
          const stream = { onmessage: null as ((event: { data: string }) => void) | null, onerror: null, close() {} }
          window.setTimeout(() => stream.onmessage?.({ data: JSON.stringify("Sandbox shell ready\r\n$ ") }), 100)
          return stream as unknown as EventSource
        }
        super(url, options)
      }
    }
  })
  let created = false
  let submittedTemplate = ""
  let submittedSource = ""
  let submittedRef = ""
  const requestedPaths: string[] = []
  const deployment = { id: "deployment-fixture", repository_full_name: "fixture-user/project", source: "tag", ref: "v1.0", commit_sha: "a".repeat(40), config_path: ".qiniu/deploy.yaml", template_id: "custom-template", status: "running", sandbox_id: "sandbox-fixture", revision_number: 2, created_at: "2026-10-13T00:00:00Z", expires_at: new Date(Date.now() + 3600000).toISOString() }
  await page.route("**/auth/session", (route) => route.fulfill({ json: { authenticated: true, oauth_enabled: true, login: "fixture-user", role: "user" } }))
  await page.route("**/user/**", async (route) => {
    const url = new URL(route.request().url())
    requestedPaths.push(url.pathname)
    if (url.pathname === "/user/github-app") return route.fulfill({ json: { installations: [{ id: 1, account_login: "fixture-user", account_type: "User", manageable: true, repositories: ["fixture-user/project"] }] } })
    if (url.pathname.endsWith("/repositories")) return route.fulfill({ json: { repositories: ["fixture-user/project"] } })
    if (url.pathname === "/user/deployment-refs") return route.fulfill({ json: { refs: url.searchParams.get("source") === "tag" ? ["v1.0"] : ["main", "example/vite"], has_more: false } })
    if (url.pathname === "/user/deployment-templates") return route.fulfill({ json: { templates: [{ template_id: "base-template", names: ["base"] }, { template_id: "custom-template", aliases: ["custom"] }] } })
    if (url.pathname === "/user/deployments" && route.request().method() === "POST") {
      submittedTemplate = route.request().postDataJSON().template_id
      submittedSource = route.request().postDataJSON().source
      submittedRef = route.request().postDataJSON().ref
      created = true
      return route.fulfill({ status: 201, json: deployment })
    }
    if (url.pathname.endsWith("/terminal") && route.request().method() === "POST") return route.fulfill({ status: 201, json: { session_id: "terminal-fixture", pid: 99, sandbox_id: "sandbox-fixture" } })
    if (url.pathname.includes("/terminal/")) {
      if (url.pathname.endsWith("/input")) terminalInputs.push(route.request().postDataJSON().data)
      if (route.request().method() === "DELETE") terminalClosed = true
      return route.fulfill({ json: { status: "ok" } })
    }
    if (url.pathname === "/user/deployments") return route.fulfill({ json: { items: created ? [deployment] : [] } })
    if (url.pathname === "/user/deployments/deployment-fixture") return route.fulfill({ json: deployment })
    if (url.pathname === "/user/deployments/deployment-fixture/resources") return route.fulfill({ json: { template_id: deployment.template_id, expires_at: deployment.expires_at } })
    if (url.pathname.endsWith("/events")) return route.fulfill({ json: { events: url.searchParams.has("after_id") ? [] : [{ id: 1, event_type: "output", stage: "installing", stream: "stdout", message: "Vite build completed", created_at: "2026-10-13T00:00:00Z" }], has_more: false } })
    return route.fulfill({ status: 404, body: "fixture route not found" })
  })
  await page.goto("/deployments", { waitUntil: "networkidle" })
  await expect(page.getByRole("heading", { name: "Deployments", exact: true })).toBeVisible()
  await expect(page.locator("form")).toHaveCount(0)
  expect(requestedPaths).toEqual(["/user/deployments"])
  const initialLoads = pageLoads()
  await page.getByRole("link", { name: "Create deployment", exact: true }).click()
  await expect(page).toHaveURL(/\/deployments\/new$/)
  await expect(page.locator("form")).toHaveCount(1)
  await expect(page.locator('[data-testid="deployment-list"]')).toHaveCount(0)
  await expect(page.locator('[data-testid="deployment-logs"]')).toHaveCount(0)
  expect(pageLoads()).toEqual(initialLoads)
  await page.goBack()
  await expect(page.getByRole("heading", { name: "Deployments", exact: true })).toBeVisible()
  await page.goForward()
  await expect(page.locator("#deployment-source")).toBeVisible()
  await page.getByRole("link", { name: "Back to deployments", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Deployments", exact: true })).toBeVisible()
  await page.getByRole("link", { name: "Create deployment", exact: true }).click()
  await expect(page.locator("#deployment-source")).toBeVisible()
  expect(pageLoads()).toEqual(initialLoads)
  await page.reload({ waitUntil: "networkidle" })
  await expect(page).toHaveURL(/\/deployments\/new$/)
  await expect(page.locator("#deployment-source")).toBeVisible()
  expect(pageLoads()).toEqual({ documents: initialLoads.documents + 1, sessions: initialLoads.sessions + 1 })
  const formLoads = pageLoads()
  await page.locator("#deployment-repository").selectOption("fixture-user/project")
  await page.locator("#deployment-template").selectOption("custom-template")
  await page.locator("#deployment-source").selectOption("tag")
  await expect(page.getByText("Select Tag", { exact: true })).toBeVisible()
  await page.locator("#deployment-ref").selectOption("v1.0")
  await page.locator('button[type="submit"]').click()
  await expect(page).toHaveURL(/\/github\/fixture-user\/project\/deployments\/deployment-fixture\?tag=v1.0$/)
  expect(submittedTemplate).toBe("custom-template")
  expect(submittedSource).toBe("tag")
  expect(submittedRef).toBe("v1.0")
  await expect(page.getByRole("heading", { name: "Deployment", exact: true })).toBeVisible()
  await expect(page.locator('[data-testid="deployment-logs"]')).toContainText("Vite build completed")
  expect(pageLoads()).toEqual(formLoads)
  await page.reload({ waitUntil: "networkidle" })
  await expect(page).toHaveURL(/\/github\/fixture-user\/project\/deployments\/deployment-fixture\?tag=v1.0$/)
  await expect(page.locator('[data-testid="deployment-logs"]')).toContainText("Vite build completed")
  await expect(page.locator("#deployment-template")).toHaveCount(0)
  const historyHeader = await page.getByTestId("deployment-history-header").boundingBox()
  const outputHeader = await page.getByTestId("deployment-output").getByRole("tablist").boundingBox()
  expect(historyHeader!.y + historyHeader!.height).toBe(outputHeader!.y + outputHeader!.height)
  await page.getByRole("tab", { name: "Terminal", exact: true }).click()
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  await expect(page.getByRole("button", { name: "Disconnect", exact: true })).toBeVisible()
  await expect(page.locator(".xterm-screen")).toBeVisible()
  const historyPanel = await page.getByTestId("deployment-history").boundingBox()
  const outputPanel = await page.getByTestId("deployment-output").boundingBox()
  expect(historyPanel!.height).toBe(outputPanel!.height)
  expect(historyPanel!.x + historyPanel!.width).toBe(outputPanel!.x)
  await page.getByTestId("deployment-workspace").screenshot({ path: testInfo.outputPath("deployment-terminal-divider.png") })
  await page.locator(".xterm-helper-textarea").focus()
  await page.keyboard.type("pwd")
  await page.keyboard.press("Enter")
  await expect.poll(() => terminalInputs.join("")).toContain("pwd\r")
  await page.getByRole("tab", { name: "Deployment logs", exact: true }).click()
  await expect.poll(() => terminalClosed).toBe(true)
  const detailLoads = pageLoads()
  await page.getByRole("button", { name: "Back to deployments" }).click()
  await expect(page).toHaveURL(/\/github\/fixture-user\/project\/deployments\?tag=v1.0$/)
  await expect(page.locator("form")).toHaveCount(0)
  await expect(page.locator('[data-testid="deployment-list"]')).toContainText("fixture-user/project")
  await page.locator('[data-testid="deployment-list"] a').click()
  await expect(page).toHaveURL(/\/github\/fixture-user\/project\/deployments\/deployment-fixture\?tag=v1.0$/)
  await expect(page.getByRole("heading", { name: "Deployment", exact: true })).toBeVisible()
  expect(pageLoads()).toEqual(detailLoads)
  await page.goto("/deployments/deployment-fixture?revision=1", { waitUntil: "networkidle" })
  await page.getByRole("tab", { name: "Terminal", exact: true }).click()
  await expect(page.getByText("The terminal is available only for the current running revision.")).toBeVisible()
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toHaveCount(0)
  await page.goto("/deployments/deployment-fixture", { waitUntil: "networkidle" })
  await page.getByRole("tab", { name: "Terminal", exact: true }).click()
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  await expect(page.getByRole("button", { name: "Disconnect", exact: true })).toBeVisible()
  terminalClosed = false
  deployment.status = "completed"
  await expect(page.getByText("The terminal is available only for the current running revision.")).toBeVisible({ timeout: 10000 })
  await expect.poll(() => terminalClosed).toBe(true)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole("tab", { name: "Deployment logs", exact: true }).click()
  await expect(page.locator('[data-testid="deployment-logs"]')).toBeVisible()
  await page.getByTestId("deployment-workspace").screenshot({ path: testInfo.outputPath("deployment-narrow-divider.png") })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.getByRole("button", { name: "Back to deployments" }).click()
  await expect(page.locator('[data-testid="deployment-list"]')).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  diagnostics.expectClean()
})

test("creates a PR deployment and reloads its GitHub context", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")
  const diagnostics = observeBrowserDiagnostics(page)
  let submitted: Record<string, unknown> = {}
  const deployment = { id: "pr-deployment", repository_full_name: "fixture-user/project", source: "pull_request", ref: "7", pull_request_number: 7, source_repository_full_name: "contributor/fork", source_ref: "feature/preview", commit_sha: "a".repeat(40), status: "queued", revision_number: 1, created_at: "2026-10-14T00:00:00Z", expires_at: "2026-10-14T01:00:00Z" }
  await page.route("**/auth/session", (route) => route.fulfill({ json: { authenticated: true, oauth_enabled: true, login: "fixture-user", role: "user" } }))
  await page.route("**/user/**", (route) => {
    const url = new URL(route.request().url())
    if (url.pathname === "/user/github-app") return route.fulfill({ json: { installations: [{ id: 1, account_login: "fixture-user", account_type: "User", manageable: true, repositories: ["fixture-user/project"] }] } })
    if (url.pathname.endsWith("/repositories")) return route.fulfill({ json: { repositories: ["fixture-user/project"] } })
    if (url.pathname === "/user/deployment-refs") return route.fulfill({ json: { refs: url.searchParams.get("source") === "pull_request" ? ["7"] : ["main"], has_more: false } })
    if (url.pathname === "/user/deployment-templates") return route.fulfill({ json: { templates: [{ template_id: "base", names: ["base"] }] } })
    if (url.pathname === "/user/deployments" && route.request().method() === "POST") {
      submitted = route.request().postDataJSON()
      return route.fulfill({ status: 201, json: deployment })
    }
    if (url.pathname === "/user/deployments/pr-deployment") return route.fulfill({ json: deployment })
    if (url.pathname.endsWith("/resources")) return route.fulfill({ json: {} })
    if (url.pathname.endsWith("/events")) return route.fulfill({ json: { events: [], has_more: false } })
    return route.fulfill({ status: 404, body: "fixture route not found" })
  })
  await page.goto("/deployments/new", { waitUntil: "networkidle" })
  await page.locator("#deployment-repository").selectOption("fixture-user/project")
  await page.locator("#deployment-source").selectOption("pull_request")
  await expect(page.getByText("Select PR", { exact: true })).toBeVisible()
  await page.locator('button[type="submit"]').click()
  await expect(page).toHaveURL(/\/github\/fixture-user\/project\/pull_requests\/7\/deployments\/pr-deployment$/)
  expect(submitted.source).toBe("pull_request")
  expect(submitted.ref).toBe("7")
  await expect(page.getByText("contributor/fork:feature/preview", { exact: true })).toBeVisible()
  await page.reload({ waitUntil: "networkidle" })
  await expect(page.getByRole("heading", { name: "Deployment", exact: true })).toBeVisible()
  diagnostics.expectClean()
})

test("keeps deployment path IDs and readable source filters through history", async ({ page }) => {
  test.skip(Boolean(process.env.RUNNERD_UI_SMOKE_BASE_URL), "local fixture coverage only")
  const diagnostics = observeBrowserDiagnostics(page)
  const pageLoads = observePageLoads(page)
  const runners = fixtureRunners(3)
  runners[0] = { ...runners[0], status: "completed", head_branch: "example/vite" }
  runners[1] = { ...runners[1], repository_full_name: runners[0].repository_full_name, head_branch: "main" }
  const selected = runners[0]
  const repository = selected.repository_full_name!
  const sha = selected.head_sha!
  const deployment = { id: "readable", repository_full_name: repository, source: "branch", ref: "example/vite", commit_sha: sha, status: "completed", revision_number: 1, created_at: selected.created_at, expires_at: selected.created_at }
  const listQueries: string[] = []
  await page.route("**/auth/session", (route) => route.fulfill({ json: { authenticated: true, oauth_enabled: true, login: "fixture", role: "user" } }))
  await page.route("**/user/**", (route) => {
    const url = new URL(route.request().url())
    if (url.pathname === "/user/runner_requests") return route.fulfill({ headers: { "X-Total-Count": "3" }, json: runners })
    if (url.pathname === "/user/github-app") return route.fulfill({ json: { installations: [{ id: 1, account_login: "fixture", account_type: "User", manageable: true, repositories: [repository] }] } })
    if (url.pathname === "/user/onboarding/product-tour") return route.fulfill({ json: { version: 1, status: "completed", tour_seen: true } })
    if (url.pathname.startsWith("/user/github/branches/")) {
      const job = runners.find((item) => item.head_sha === url.pathname.split("/").at(-2))!
      return route.fulfill({ json: { key: `branch:${repository}:${job.head_branch}:${job.head_sha}`, group: "branch", repository, title: job.head_branch, head_branch: job.head_branch, head_sha: job.head_sha, jobs: [job], current_jobs: [job], previous_jobs: [], workflow_run_ids: [job.workflow_run_id] } })
    }
    if (url.pathname === "/user/deployments") {
      listQueries.push(url.search)
      return route.fulfill({ json: { items: [deployment] } })
    }
    if (url.pathname === "/user/deployments/readable") return route.fulfill({ json: deployment })
    if (url.pathname.endsWith("/resources")) return route.fulfill({ json: {} })
    if (url.pathname.endsWith("/events")) return route.fulfill({ json: { events: [], has_more: false } })
    if (url.pathname.endsWith("/repositories")) return route.fulfill({ json: { repositories: [repository] } })
    if (url.pathname === "/user/deployment-refs") return route.fulfill({ json: { refs: ["main", "example/vite"] } })
    if (url.pathname === "/user/deployment-templates") return route.fulfill({ json: { templates: [{ template_id: "base", names: ["base"] }] } })
    return route.fulfill({ status: 404, body: "fixture route not found" })
  })
  await page.goto(`/github/${repository}/jobs`, { waitUntil: "networkidle" })
  await expect(page.locator("main aside button").filter({ hasText: repository })).toHaveCount(2)
  await expect(page.getByText("fixture/repository-2", { exact: true })).toHaveCount(0)
  await page.goto(`/github/${repository}/jobs?branch=missing`, { waitUntil: "networkidle" })
  await expect(page.locator("main aside button").filter({ hasText: repository })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Deploy this commit", exact: true })).toHaveCount(0)
  await page.goto(`/github/${repository}/branch/example%2Fvite/jobs?commit=${sha}&job=${selected.id}`, { waitUntil: "networkidle" })
  const jobURL = `/github/${repository}/jobs?branch=example/vite&commit=${sha}&job=${selected.id}`
  await expect(page).toHaveURL(new URL(jobURL, page.url()).href)
  await expect(page.locator("main aside button").filter({ hasText: repository })).toHaveCount(1)
  await page.reload({ waitUntil: "networkidle" })
  const jobLoads = pageLoads()
  await page.getByRole("link", { name: "Deployment history", exact: true }).click()
  const historyURL = `/github/${repository}/deployments?branch=example/vite`
  expect(new URL(page.url()).pathname + new URL(page.url()).search).toBe(historyURL)
  await expect(page.locator('[data-testid="deployment-list"] a')).toHaveCount(1)
  expect(pageLoads()).toEqual(jobLoads)
  const listQuery = new URLSearchParams(listQueries.at(-1))
  expect(listQuery.get("repository_full_name")).toBe(repository)
  expect(listQuery.get("ref")).toBe("refs/heads/example/vite")
  expect(listQuery.get("commit_sha")).toBeNull()
  await page.locator('[data-testid="deployment-list"] a').click()
  await expect(page.getByRole("heading", { name: "Deployment", exact: true })).toBeVisible()
  await page.goBack({ waitUntil: "networkidle" })
  expect(new URL(page.url()).pathname + new URL(page.url()).search).toBe(historyURL)
  await page.goForward({ waitUntil: "networkidle" })
  await expect(page.getByRole("heading", { name: "Deployment", exact: true })).toBeVisible()
  expect(new URL(page.url()).searchParams.has("commit")).toBe(false)
  expect(pageLoads()).toEqual(jobLoads)
  await page.reload({ waitUntil: "networkidle" })
  expect(new URL(page.url()).searchParams.has("commit")).toBe(false)
  await page.goto(`${historyURL}&commit=${sha}`, { waitUntil: "networkidle" })
  expect(new URL(page.url()).pathname + new URL(page.url()).search).toBe(`${historyURL}&commit=${sha}`)
  expect(new URLSearchParams(listQueries.at(-1)).get("commit_sha")).toBe(sha)
  await page.goto(`/github/${repository}/deployments`, { waitUntil: "networkidle" })
  expect(new URLSearchParams(listQueries.at(-1)).get("ref")).toBeNull()
  await page.getByRole("link", { name: "Create deployment", exact: true }).click()
  await expect(page.locator("#deployment-repository")).toHaveValue(repository)
  await expect(page.locator("#deployment-ref")).toHaveValue("main")
  for (const retiredPath of [
    `${historyURL}&deployment=readable`,
    `/github/${repository}/branch/example%2Fvite/deployments`,
  ]) {
    await page.goto(retiredPath, { waitUntil: "networkidle" })
    await expect(page.getByRole("heading", { name: "Page not found", exact: true })).toBeVisible()
  }
  diagnostics.expectClean()
})
