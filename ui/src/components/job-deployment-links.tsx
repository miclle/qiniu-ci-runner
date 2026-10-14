import { useTranslation } from "react-i18next"
import type { RunnerState } from "@/admin-types"
import { Button } from "@/components/ui/button"
import { deploymentCreatePath, githubContextPath, jobDeploymentContext } from "@/github-context-routes"

export function JobDeploymentLinks({ job }: { job: RunnerState }) {
  const { t } = useTranslation()
  const context = jobDeploymentContext(job)
  if (!context) return null
  return <div className="flex flex-wrap items-center gap-2">
    <Button asChild variant="outline" size="sm"><a href={githubContextPath(context, "deployments")}>{t("user.deploymentHistory")}</a></Button>
    <Button asChild size="sm"><a href={deploymentCreatePath(context, job.head_sha)}>{t("user.deployThisCommit")}</a></Button>
  </div>
}
