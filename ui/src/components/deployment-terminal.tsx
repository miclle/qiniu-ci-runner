import { Loader2, SquareTerminal } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { useSandboxTerminal } from "@/hooks/use-sandbox-terminal"

type Props = {
  endpoint: string
  request: (url: string, options?: RequestInit) => Promise<unknown>
}

export default function DeploymentTerminal({ endpoint, request }: Props) {
  const { t } = useTranslation()
  const { terminalEl, terminalSession, terminalConnecting, terminalError, connectTerminal, closeTerminalSession } = useSandboxTerminal({
    endpoint,
    available: true,
    request,
    connectingMessage: t("user.connectingTerminal"),
    streamDisconnectedMessage: t("user.terminalDisconnected"),
    connectErrorMessage: t("user.terminalConnectFailed"),
  })

  return <Card className="gap-4 py-4">
    <CardHeader className="px-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <CardTitle>{t("common.terminal")}</CardTitle>
        <Button type="button" size="sm" variant="outline" disabled={terminalConnecting} onClick={() => terminalSession ? closeTerminalSession() : void connectTerminal()}>
          {terminalConnecting ? <Loader2 className="h-4 w-4 animate-spin" /> : <SquareTerminal className="h-4 w-4" />}
          {terminalSession ? t("user.deploymentTerminalDisconnect") : terminalConnecting ? t("common.connecting") : t("common.connect")}
        </Button>
      </div>
      <CardDescription>{t("user.deploymentTerminalDescription")}</CardDescription>
    </CardHeader>
    <CardContent className="space-y-3 px-5">
      <div className="relative rounded-md bg-[#111318] p-2">
        <div ref={terminalEl} className="h-[400px] min-w-0 overflow-hidden" />
        {!terminalSession && !terminalConnecting ? <p className="pointer-events-none absolute inset-0 flex items-center justify-center px-4 text-center text-sm text-slate-400">{t("user.connectInteractiveShell")}</p> : null}
      </div>
      {terminalError ? <p role="alert" className="text-sm text-destructive">{terminalError}</p> : null}
    </CardContent>
  </Card>
}
