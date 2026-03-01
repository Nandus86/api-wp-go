import { Save } from 'lucide-react'

export function SettingsPage() {
    return (
        <div className="p-6 space-y-8 max-w-4xl mx-auto">
            <div>
                <h1 className="text-2xl font-semibold text-foreground">Settings</h1>
                <p className="text-sm text-muted-foreground mt-1">Manage global preferences and API configuration.</p>
            </div>

            {/* General Settings */}
            <section className="bg-card border border-border rounded-xl overflow-hidden">
                <div className="px-6 py-4 border-b border-border bg-muted/20">
                    <h2 className="text-base font-semibold text-foreground">General</h2>
                </div>
                <div className="p-6 space-y-6">
                    <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                        <div className="md:col-span-1">
                            <label className="text-sm font-medium text-foreground">Theme</label>
                            <p className="text-xs text-muted-foreground mt-1">Customize the platform appearance.</p>
                        </div>
                        <div className="md:col-span-2 flex gap-3">
                            <button className="flex-1 py-2 px-4 border-2 border-primary bg-background rounded-lg text-sm font-medium">
                                Dark Mode
                            </button>
                            <button disabled className="flex-1 py-2 px-4 border border-border bg-muted/30 rounded-lg text-sm font-medium text-muted-foreground opacity-50 cursor-not-allowed">
                                Light Mode
                            </button>
                        </div>
                    </div>

                    <hr className="border-border" />

                    <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
                        <div className="md:col-span-1">
                            <label className="text-sm font-medium text-foreground">Global Webhook</label>
                            <p className="text-xs text-muted-foreground mt-1">Fallback URL for instances without a specific webhook.</p>
                        </div>
                        <div className="md:col-span-2">
                            <input
                                type="text"
                                placeholder="https://api.example.com/webhook"
                                className="w-full px-3 py-2 bg-background border border-border rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-ring"
                            />
                        </div>
                    </div>
                </div>
            </section>

            {/* API Keys */}
            <section className="bg-card border border-border rounded-xl overflow-hidden">
                <div className="px-6 py-4 border-b border-border bg-muted/20 flex justify-between items-center">
                    <h2 className="text-base font-semibold text-foreground">API Keys</h2>
                    <button className="px-3 py-1 bg-secondary text-secondary-foreground text-xs font-medium rounded-md hover:bg-secondary/80">
                        Generate New Key
                    </button>
                </div>
                <div className="p-6">
                    <div className="bg-background border border-border rounded-md p-4 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-foreground">Production Key</p>
                            <p className="text-xs font-mono text-muted-foreground mt-1">sk_live_***************************</p>
                        </div>
                        <button className="text-sm text-primary hover:underline font-medium">Revoke</button>
                    </div>
                </div>
            </section>

            <div className="flex justify-end pt-4">
                <button className="flex items-center gap-2 px-6 py-2.5 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:bg-primary/90 transition-colors shadow-sm">
                    <Save size={16} />
                    Save Changes
                </button>
            </div>
        </div>
    )
}
