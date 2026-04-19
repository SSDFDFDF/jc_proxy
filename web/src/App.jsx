import { AdminShell } from './layout/AdminShell'
import { useAdminConsole } from './hooks/useAdminConsole'
import { LoginPage } from './pages/LoginPage'
import { OverviewPage } from './pages/OverviewPage'
import { RawPage } from './pages/RawPage'
import { SecurityPage } from './pages/SecurityPage'
import { KeyHubPage } from './pages/KeyHubPage'
import { SystemPage } from './pages/SystemPage'
import { VendorsPage } from './pages/VendorsPage'

function App() {
  const consoleState = useAdminConsole()
  const { auth, shell, overview, config, upstream, statsView, security, actions } = consoleState

  if (!auth.isAuthed) {
    return (
      <LoginPage
        username={auth.username}
        password={auth.password}
        onUsernameChange={auth.setUsername}
        onPasswordChange={auth.setPassword}
        onLogin={auth.login}
        busy={auth.busy}
        notice={auth.notice}
      />
    )
  }

  return (
    <AdminShell
      navItems={shell.navItems}
      nav={shell.nav}
      onNavChange={shell.setNav}
      me={auth.me}
      username={auth.username}
      lastSyncAt={auth.lastSyncAt}
      notice={auth.notice}
      currentPageMeta={shell.currentPageMeta}
      token={auth.token}
      upstreamStorage={upstream.upstreamKeysData.storage}
      busy={auth.busy}
      onRefreshAll={() => actions.refreshAll(config.selectedVendor, upstream.selectedKeyVendor)}
      onRefreshStats={() => statsView.loadStats(false, true)}
      onLogout={auth.logout}
    >
      {shell.nav === 'overview' && (
        <OverviewPage
          metrics={overview.overviewMetrics}
          vendorRows={overview.vendorRows}
          upstreamStorage={upstream.upstreamKeysData.storage}
          onOpenUpstreamKeys={() => shell.setNav('keyHub')}
        />
      )}

      {shell.nav === 'keyHub' && (
        <KeyHubPage
          upstreamKeysData={upstream.upstreamKeysData}
          selectedKeyVendor={upstream.selectedKeyVendor}
          showSecrets={upstream.showSecrets}
          busy={auth.busy}
          onToggleSecrets={() => upstream.setShowSecrets((prev) => !prev)}
          onSelectVendor={upstream.selectKeyVendor}
          onAddKeys={upstream.addUpstreamKeys}
          onEnableKey={upstream.enableUpstreamKey}
          onDisableKey={upstream.disableUpstreamKey}
          onDeleteKey={upstream.deleteUpstreamKey}
          vendorRows={overview.vendorRows}
          runtimeStats={statsView.stats}
          autoRefreshStats={statsView.autoRefreshStats}
          refreshEverySec={statsView.refreshEverySec}
          onToggleAutoRefresh={() => statsView.setAutoRefreshStats((prev) => !prev)}
          onRefreshEverySecChange={statsView.setRefreshEverySec}
          onRefreshStats={() => statsView.loadStats(false, true)}
        />
      )}

      {shell.nav === 'vendors' && (
        <VendorsPage
          busy={auth.busy}
          vendorRows={overview.vendorRows}
          selectedVendor={config.selectedVendor}
          vendorDraft={config.vendorDraft}
          vendorBackoffDuration={config.vendorBackoffDuration}
          invalidKeyStatusCodesText={config.invalidKeyStatusCodesText}
          invalidKeyKeywordsText={config.invalidKeyKeywordsText}
          responseRuleRows={config.responseRuleRows}
          failoverResponseStatusCodesText={config.failoverResponseStatusCodesText}
          upstreamBodyTimeoutText={config.upstreamBodyTimeoutText}
          clientHeaderPreset={config.clientHeaderPreset}
          allowlistText={config.allowlistText}
          dropHeadersText={config.dropHeadersText}
          injectRows={config.injectRows}
          rewriteRows={config.rewriteRows}
          newVendorForm={config.newVendorForm}
          onSelectVendor={config.selectVendor}
          onRefresh={() => actions.refreshAll(config.selectedVendor, upstream.selectedKeyVendor)}
          onNewVendorFormChange={config.setNewVendorForm}
          onCreateVendor={config.createVendor}
          onOpenUpstreamKeys={() => shell.setNav('keyHub')}
          onSaveVendor={config.saveVendor}
          onDeleteVendor={config.deleteVendor}
          onMutateVendorDraft={config.mutateVendorDraft}
          onVendorBackoffDurationChange={config.setVendorBackoffDuration}
          onInvalidKeyStatusCodesTextChange={config.setInvalidKeyStatusCodesText}
          onInvalidKeyKeywordsTextChange={config.setInvalidKeyKeywordsText}
          setResponseRuleRows={config.setResponseRuleRows}
          onFailoverResponseStatusCodesTextChange={config.setFailoverResponseStatusCodesText}
          onUpstreamBodyTimeoutTextChange={config.setUpstreamBodyTimeoutText}
          onClientHeaderPresetChange={config.setClientHeaderPreset}
          onAllowlistTextChange={config.setAllowlistText}
          onDropHeadersTextChange={config.setDropHeadersText}
          setInjectRows={config.setInjectRows}
          setRewriteRows={config.setRewriteRows}
        />
      )}



      {shell.nav === 'system' && (
        <SystemPage
          busy={auth.busy}
          systemForm={config.systemForm}
          maskedConfig={config.maskedConfig}
          onSystemFormChange={config.setSystemForm}
          onSave={config.saveSystem}
          onRefreshPreview={() => actions.refreshAll(config.selectedVendor, upstream.selectedKeyVendor)}
        />
      )}

      {shell.nav === 'security' && (
        <SecurityPage
          busy={auth.busy}
          me={auth.me}
          username={auth.username}
          token={auth.token}
          lastSyncAt={auth.lastSyncAt}
          maskedConfig={config.maskedConfig}
          newPassword={security.newPassword}
          onNewPasswordChange={security.setNewPassword}
          onVerifySession={auth.verifySession}
          onLogout={auth.logout}
          onRotatePassword={security.rotatePassword}
        />
      )}

      {shell.nav === 'raw' && (
        <RawPage
          busy={auth.busy}
          rawConfigText={config.rawConfigText}
          onRawConfigTextChange={config.setRawConfigText}
          onReload={() => actions.refreshAll(config.selectedVendor, upstream.selectedKeyVendor)}
          onSave={config.saveRaw}
        />
      )}
    </AdminShell>
  )
}

export default App
