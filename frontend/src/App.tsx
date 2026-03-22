import { useState } from "react";
import { toast } from "sonner";
import { ThemeProvider } from "@/components/theme-provider";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Toaster } from "@/components/ui/sonner";
import { Sidebar } from "@/components/layout/Sidebar";
import { AssetTree } from "@/components/layout/AssetTree";
import { MainPanel } from "@/components/layout/MainPanel";
import { AIPanel } from "@/components/layout/AIPanel";
import { AssetForm } from "@/components/asset/AssetForm";
import { GroupDialog } from "@/components/asset/GroupDialog";
import { ConnectDialog } from "@/components/terminal/ConnectDialog";
import { useAssetStore } from "@/stores/assetStore";
import { useTerminalStore } from "@/stores/terminalStore";
import { asset_entity } from "../wailsjs/go/models";

function App() {
  const [activePage, setActivePage] = useState("home");
  const [assetTreeCollapsed] = useState(false);
  const [aiPanelCollapsed, setAiPanelCollapsed] = useState(false);

  // 资产表单
  const [assetFormOpen, setAssetFormOpen] = useState(false);
  const [editingAsset, setEditingAsset] = useState<asset_entity.Asset | null>(null);
  const [defaultGroupId, setDefaultGroupId] = useState(0);

  // 分组对话框
  const [groupDialogOpen, setGroupDialogOpen] = useState(false);

  // SSH 连接对话框
  const [connectDialogOpen, setConnectDialogOpen] = useState(false);
  const [connectingAsset, setConnectingAsset] = useState<asset_entity.Asset | null>(null);

  const { assets, selectedAssetId, selectAsset, deleteAsset } = useAssetStore();
  const { connect } = useTerminalStore();
  const selectedAsset = assets.find((a) => a.ID === selectedAssetId) || null;

  const handleAddAsset = (groupId?: number) => {
    setEditingAsset(null);
    setDefaultGroupId(groupId ?? 0);
    setAssetFormOpen(true);
  };

  const handleEditAsset = (asset: asset_entity.Asset) => {
    setEditingAsset(asset);
    setAssetFormOpen(true);
  };

  const handleSelectAsset = (asset: asset_entity.Asset) => {
    selectAsset(asset.ID);
  };

  const handleDeleteAsset = async (id: number) => {
    await deleteAsset(id);
  };

  const handleConnectAsset = (asset: asset_entity.Asset) => {
    setConnectingAsset(asset);
    setConnectDialogOpen(true);
  };

  const handleConnect = async (password: string) => {
    if (!connectingAsset) return;
    try {
      await connect(connectingAsset.ID, connectingAsset.Name, password, 80, 24);
    } catch (e) {
      toast.error(String(e));
    }
  };

  const connectingAuthType = (() => {
    if (!connectingAsset) return "password";
    try {
      const cfg = JSON.parse(connectingAsset.Config || "{}") as { auth_type?: string };
      return cfg.auth_type || "password";
    } catch {
      return "password";
    }
  })();

  return (
    <ThemeProvider defaultTheme="system">
      <TooltipProvider>
        <div className="flex h-screen w-screen overflow-hidden bg-background">
          <Sidebar activePage={activePage} onPageChange={setActivePage} />
          <AssetTree
            collapsed={assetTreeCollapsed}
            onAddAsset={handleAddAsset}
            onAddGroup={() => setGroupDialogOpen(true)}
            onSelectAsset={handleSelectAsset}
          />
          <MainPanel
            activePage={activePage}
            selectedAsset={selectedAsset}
            onEditAsset={handleEditAsset}
            onDeleteAsset={handleDeleteAsset}
            onConnectAsset={handleConnectAsset}
          />
          <AIPanel
            collapsed={aiPanelCollapsed}
            onToggle={() => setAiPanelCollapsed(!aiPanelCollapsed)}
          />
        </div>

        <AssetForm
          open={assetFormOpen}
          onOpenChange={setAssetFormOpen}
          editAsset={editingAsset}
          defaultGroupId={defaultGroupId}
        />
        <GroupDialog
          open={groupDialogOpen}
          onOpenChange={setGroupDialogOpen}
        />
        <ConnectDialog
          open={connectDialogOpen}
          onOpenChange={setConnectDialogOpen}
          assetName={connectingAsset?.Name || ""}
          authType={connectingAuthType}
          onConnect={handleConnect}
        />
        <Toaster richColors />
      </TooltipProvider>
    </ThemeProvider>
  );
}

export default App;
