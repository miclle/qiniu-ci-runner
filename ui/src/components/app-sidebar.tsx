import { Activity, CirclePlus, Github, ListTree, RefreshCw, Server, Terminal } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar"

type AppSidebarProps = {
  connected: boolean
  activeCount: number
  onRefresh: () => void
  onCreateFocus: () => void
  onClearToken: () => void
}

export function AppSidebar({
  connected,
  activeCount,
  onRefresh,
  onCreateFocus,
  onClearToken,
}: AppSidebarProps) {
  return (
    <Sidebar collapsible="offcanvas">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton className="data-[slot=sidebar-menu-button]:!p-1.5">
              <div className="flex items-center gap-2">
                <div className="rounded-lg bg-sidebar-primary p-1.5 shadow-sm">
                  <Terminal className="h-4 w-4 text-sidebar-primary-foreground" />
                </div>
                <div className="flex flex-col">
                  <span className="bg-gradient-to-r from-primary to-primary/70 bg-clip-text text-base font-semibold text-transparent">
                    E2B
                  </span>
                  <span className="text-[10px] font-medium leading-none text-muted-foreground">
                    Runner
                  </span>
                </div>
              </div>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton isActive className="data-[active=true]:bg-primary/10 data-[active=true]:text-primary data-[active=true]:shadow-sm">
                  <ListTree className="text-sidebar-primary" />
                  <span className="font-medium">Runners</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton onClick={onCreateFocus}>
                  <CirclePlus className="text-sidebar-primary" />
                  <span className="font-medium">Create</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton onClick={onRefresh}>
                  <RefreshCw className="text-sidebar-primary" />
                  <span className="font-medium">Refresh</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>

      <SidebarFooter>
        <div className="rounded-lg border bg-card p-2 shadow-sm transition-colors hover:bg-muted/50">
          <div className="mb-2 flex items-center gap-2 px-1">
            <span className={connected ? "h-2 w-2 rounded-full bg-green-500" : "h-2 w-2 rounded-full bg-muted-foreground"} />
            <span className="min-w-0 flex-1 truncate text-xs font-medium text-muted-foreground">
              {connected ? "Connected" : "Locked"}
            </span>
          </div>
          <div className="grid gap-2 rounded-md bg-background p-2 text-xs">
            <div className="flex items-center justify-between gap-2">
              <span className="inline-flex items-center gap-1 text-muted-foreground">
                <Activity className="h-3.5 w-3.5" />
                Active
              </span>
              <span className="font-semibold">{activeCount}</span>
            </div>
            <div className="flex items-center justify-between gap-2">
              <span className="inline-flex items-center gap-1 text-muted-foreground">
                <Server className="h-3.5 w-3.5" />
                Scope
              </span>
              <span className="font-semibold">repo/org</span>
            </div>
            <div className="flex items-center justify-between gap-2">
              <span className="inline-flex items-center gap-1 text-muted-foreground">
                <Github className="h-3.5 w-3.5" />
                Actions
              </span>
              <span className="font-semibold">ephemeral</span>
            </div>
          </div>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="mt-2 w-full"
            onClick={onClearToken}
          >
            Clear token
          </Button>
        </div>
      </SidebarFooter>
    </Sidebar>
  )
}
