---
stepsCompleted: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14]
inputDocuments:
  - README.md
  - implementation_plan.md (conversation artifact)
  - frontend/public/index.html (current UI)
designSystemReference: shadcn/ui (https://github.com/shadcn-ui/ui)
---

# UX Design Specification — whatsmeow-basileia

**Author:** Fernando
**Date:** 2026-02-27

---

## Executive Summary

### Project Vision

**WhatsMeow Basileia** is an internal WhatsApp instance management platform for automation teams. The panel must function as an **operational command center** — where the team can instantly see what's running and what needs attention. Functionality over aesthetics, but with professional polish that inspires confidence.

**Design System Reference:** shadcn/ui — clean, minimal, functional components with professional finish.

### Target Users

| Aspect | Detail |
|--------|--------|
| **Who** | Internal technical/operational team |
| **Skill level** | Intermediate — understand APIs but are not developers |
| **Device** | Desktop (optimize for 1920x1080+) |
| **Frequency** | Daily access for continuous monitoring |
| **Primary actions** | Check status, reconnect fallen instances, register new devices |

### Key Design Challenges

1. **Real-time operational visibility** — The team needs to see at a glance which instances are online, offline, or problematic. The current table doesn't scale well with many instances.
2. **Message flow monitoring** — No existing view of message volume (incoming/outgoing). The team operates blind without knowing if traffic is flowing normally.
3. **Multi-section navigation** — The current panel is a single page. Adding Dashboard, Logs, and Settings requires clean navigation.
4. **Quick actions vs. dense information** — Balance an interface that shows extensive data (webhooks, proxies, status, metrics) without becoming cluttered.

### Design Opportunities

1. **Dashboard with visual health-check** — Summary cards at the top (total instances, online, offline, messages/hour) functioning as operational traffic lights.
2. **Message flow graphs** — Timeline of sent vs. received messages per instance and overall, enabling anomaly detection (e.g., instance stopped receiving = possible ban).
3. **Instance status cards** — Instead of a flat table, compact cards with visual health indicator (green/yellow/red) with contextual actions.
4. **Centralized settings** — Default proxy and default webhook as global fallback, eliminating manual repetition.

## Core User Experience

### Defining Experience

The core experience follows a **drill-down pattern**: Dashboard → Instances → Detail.

1. **Morning Check (30 seconds):** User opens the panel and immediately sees a dashboard with health summary cards — total instances, how many online/offline, messages flowing. Green = all good. Red = action needed.
2. **Problem Hunt:** From the dashboard, user clicks into the Instances view to identify and fix specific problems (reconnect, check proxy, update webhook).
3. **Monitoring Loop:** Throughout the day, the dashboard stays open as a passive monitor. Graphs show message flow trends — a sudden drop means something broke.

### Platform Strategy

| Decision | Choice |
|----------|--------|
| **Platform** | Web application (desktop-optimized, 1920x1080+) |
| **Input** | Mouse/keyboard primary |
| **Framework** | Vanilla HTML/CSS/JS with shadcn/ui design language |
| **Navigation** | Left sidebar (collapsible) — best for ops dashboards with multiple sections |
| **Sections** | Dashboard, Instances, Logs, Settings |
| **Real-time** | WebSocket for live status updates |

**Navigation Layout (Sidebar):**
```
┌──────────┬─────────────────────────────────┐
│ 📊 Dash  │                                 │
│ 📱 Inst  │      Main Content Area          │
│ 📋 Logs  │                                 │
│ ⚙️ Cfg   │                                 │
└──────────┴─────────────────────────────────┘
```

### Effortless Interactions

1. **Auto-reconnect with notification** — Instances reconnect automatically when they drop. The panel shows a subtle toast notification: "Instance X reconnected automatically." User can intervene manually if needed (force disconnect, change proxy, etc).
2. **One-click actions** — Every critical action (reconnect, scan QR, copy webhook URL) is max 1 click away from the instance card.
3. **Smart defaults** — Global proxy and webhook configured once in Settings; new instances inherit automatically unless overridden.

### Critical Success Moments

1. **First Glance** — Opening the dashboard and knowing in under 3 seconds if everything is healthy. Cards must be unambiguous (green/red, not percentages).
2. **Anomaly Detection** — Noticing a message flow drop on the graph before a customer complains. The graph must show the last 24h with clear visual contrast between sent/received.
3. **Quick Recovery** — Instance goes down → user sees it red → clicks reconnect → sees it go green. Under 10 seconds total interaction.
4. **New Client Onboarding** — Creating a new instance with name + webhook + proxy + QR scan must be a single smooth flow, not 4 separate operations.

### Experience Principles

1. **"Traffic Light" Clarity** — Every status must be instantly readable without hover or clicks. Green = good, Yellow = warning, Red = action needed.
2. **Data, Not Decoration** — Every visual element must convey information. No ornamental UI. Charts serve monitoring, not aesthetics.
3. **Progressive Disclosure** — Dashboard shows the overview, Instance view shows the detail. User drills down only when needed.
4. **Autonomous System** — The platform should self-heal (auto-reconnect) and only demand attention when human judgment is required.

## Desired Emotional Response

### Primary Emotional Goals

**Primary Emotion: TOTAL CONTROL** — Open the panel → know exactly what's happening → act fast. The feeling of a pilot in a cockpit: many instruments, but each in the right place.

**Secondary Emotions:**
- **Confidence** — The system works autonomously (auto-reconnect). No babysitting needed.
- **Efficiency** — Problem resolved in 2 clicks, no navigating through 5 screens.
- **Calm** — Green dashboard = can take my coffee in peace.

**Emotions to AVOID:**
- ❌ Anxiety — "Is it working? I can't tell..."
- ❌ Frustration — "Where is that button again?"
- ❌ Distrust — "Is this data up to date?"

### Emotional Journey Mapping

| Stage | Emotional State | Design Support |
|-------|-----------------|----------------|
| Opens panel | Calm confidence | Green health cards, clean layout |
| Spots a red card | Focused alertness | High contrast status, clear action button |
| Clicks reconnect | Controlled expectation | Pulsing dots animation during reconnect |
| Sees it go green | Quick satisfaction | Smooth color transition, subtle success toast |
| Monitoring throughout day | Background calm | Stable layout, real-time updates without jarring reloads |

### Visual Identity

| Decision | Choice |
|----------|--------|
| **Theme** | Dark mode (default for ops/monitoring — reduced eye fatigue during prolonged use) |
| **Palette** | Dark neutrals (zinc/slate) + color accents ONLY for status |
| **Status colors** | `#22c55e` Green (online) · `#eab308` Yellow (warning) · `#ef4444` Red (error) |
| **Typography** | Inter (same as shadcn/ui) — clean, legible, professional |
| **Spacing** | Generous — no cramped interface, every element breathes |
| **Borders** | Subtle rounding (6-8px), cards with thin `border-zinc-800` |
| **Animations** | Minimal and functional — only smooth status transitions (pulse on reconnecting) |

### Emotional Design Principles

1. **Clarity Over Beauty** — Every visual choice must reduce cognitive load. If it doesn't help the user understand faster, remove it.
2. **Status as Color, Not Text** — Green/yellow/red communicates instantly. Text labels are secondary confirmation.
3. **Predictable Interactions** — Same action always produces same result in same location. Zero surprises.
4. **Silent Confidence** — The system works in the background. Only surface information when human attention is needed.

## UX Pattern Analysis & Inspiration

### Inspiring Products Analysis

**1. Grafana (Monitoring Dashboard)**
- Strength: Grid layout with metric cards, time-series graphs, native dark mode. Gold standard for operational dashboards.
- Applicable pattern: "Health cards row at top + timeline chart below" structure for our Dashboard page.

**2. Vercel Dashboard (Deploy Platform)**
- Strength: Minimalist sidebar, colored status badges for deploys, clean layout that scales with many projects. Built with shadcn/ui.
- Applicable pattern: Collapsible sidebar, status cards per project (= our instances), "professional without fluff" aesthetic.

**3. Linear (Project Management)**
- Strength: Keyboard shortcuts, fast transitions, zero visible loading spinners. The fastest and cleanest interface in the market.
- Applicable pattern: "Every pixel serves a purpose" philosophy. Contextual actions appear only on card hover.

**4. Evolution API (Direct Competitor)**
- Adequate: Complete feature set, functional instance table.
- Avoid: Visually generic interface, no monitoring graphs, scattered actions, bureaucratic form-based UX.

### Transferable UX Patterns

| Pattern | Origin | Application in WhatsMeow |
|---------|--------|--------------------------|
| **Health cards row** | Grafana | Dashboard top: Total, Online, Offline, Msgs/h |
| **Timeline chart** | Grafana | 24h chart of sent vs received messages |
| **Sidebar nav** | Vercel | Navigation: Dashboard, Instances, Logs, Settings |
| **Project cards** | Vercel | Instance cards with status badge and quick actions |
| **Contextual actions** | Linear | Buttons appear on hover (Reconnect, Edit, Delete) |
| **Toast notifications** | Linear/Vercel | Action feedback (reconnected, webhook updated) |

### Anti-Patterns to Avoid

1. **Cascading modals** (Evolution API) — No modal inside modal. Quick actions should be inline.
2. **Infinite table without filter** — With 50+ instances, flat table is unreadable. Cards with filter/search are better.
3. **Long forms for instance creation** — Must be a quick flow: name → webhook → proxy → QR. All in a compact wizard or side drawer.
4. **Polling instead of WebSocket** — Interface that doesn't update in real-time loses operator trust.

### Design Inspiration Strategy

**Adopt:**
- Health card row pattern (Grafana) — directly supports "30-second morning check" core experience
- Sidebar navigation (Vercel) — clean separation of Dashboard, Instances, Logs, Settings
- Dark mode with zinc palette (shadcn/ui) — professional ops aesthetic with reduced eye fatigue

**Adapt:**
- Instance cards (Vercel project cards) — modify to show WhatsApp-specific data (phone number, message count, proxy status)
- Timeline charts (Grafana) — simplify to show only sent vs received with clear anomaly highlighting

**Avoid:**
- Evolution API's form-heavy approach — conflicts with "2-click efficiency" principle
- Generic Bootstrap/admin template look — conflicts with "professional and unique" identity

## Design System Foundation

### Design System Choice

**React (Vite) + shadcn/ui + Tailwind CSS + Recharts**

Moving from vanilla HTML/CSS/JS single-file architecture to a proper component-based React application. The shadcn/ui library provides native React components that can be customized via Tailwind CSS, delivering professional polish with minimal effort.

### Tech Stack

| Layer | Technology | Rationale |
|-------|-----------|-----------|
| **Framework** | React 18+ (Vite) | Component architecture, state management, fast HMR |
| **Build** | Vite | Generates static HTML/JS/CSS served by existing Nginx |
| **UI Components** | shadcn/ui | Copy-paste React components, full customization control |
| **Styling** | Tailwind CSS | Required by shadcn/ui, utility-first, fast iteration |
| **Charts** | Recharts | shadcn/ui recommended, dark mode native, responsive |
| **Routing** | React Router | Client-side navigation between Dashboard/Instances/Logs/Settings |
| **Real-time** | Native WebSocket | Already implemented in Go backend |
| **Icons** | Lucide React | Default icon set for shadcn/ui |

### Design Tokens

| Token | Value | CSS Variable |
|-------|-------|-------------|
| Background | `#09090b` (zinc-950) | `--background` |
| Card/Surface | `#18181b` (zinc-900) | `--card` |
| Border | `#27272a` (zinc-800) | `--border` |
| Text Primary | `#fafafa` (zinc-50) | `--foreground` |
| Text Secondary | `#a1a1aa` (zinc-400) | `--muted-foreground` |
| Accent/Primary | `#3b82f6` (blue-500) | `--primary` |
| Success | `#22c55e` (green-500) | `--success` |
| Warning | `#eab308` (yellow-500) | `--warning` |
| Destructive | `#ef4444` (red-500) | `--destructive` |
| Font | Inter, system-ui | `--font-sans` |
| Radius | 8px cards, 6px buttons | `--radius` |
| Spacing Unit | 4px | `--spacing` |

### Component Library Plan

| Component | shadcn/ui Base | Customization |
|-----------|---------------|---------------|
| `Sidebar` | sidebar component | Collapsible, 4 nav items with icons |
| `StatCard` | card component | Metric value + label + status color border |
| `InstanceCard` | card component | Status badge + phone + actions on hover |
| `LineChart` | chart component (Recharts) | 24h timeline, sent vs received |
| `Toast` | toast/sonner | Action confirmations, auto-reconnect alerts |
| `Dialog` | dialog component | QR Code scan, create instance wizard |
| `Sheet/Drawer` | sheet component | Edit webhook/proxy side panel |
| `Badge` | badge component | Online/Offline/Warning status indicators |
| `Input + Search` | input component | Filter/search instances |
| `DataTable` | table component | Logs view with sorting and filtering |

### Implementation Approach

The Vite build output (static HTML/JS/CSS) replaces the current `frontend/public/` directory. The Nginx configuration remains identical — it serves static files and proxies `/api/` to the Go backend. No SSR needed.

## Defining Core Experience

### Defining Experience: "Glance → Spot → Fix"

The WhatsMeow Basileia experience in one sentence: *"Open the panel, glance at the status, and fix any issue in 2 clicks."*

The team thinks of the panel as a **traffic light control center**. They don't want to explore — they want to *confirm* everything is running, and act fast when it's not.

### User Mental Model

Users approach the panel as operators, not explorers. They bring the mental model of a monitoring dashboard (like server health panels). They expect:
- Instant visual status without reading text
- Problems to be highlighted, not hidden in tables
- Actions available where the problem is shown, not in a separate menu

### Experience Mechanics

```
1. INITIATION (0-3 seconds)
   └─ User opens Dashboard
   └─ 4 StatCards load instantly:
      ┌─────────┬─────────┬──────────┬──────────┐
      │ 📱 12   │ 🟢 10   │ 🔴 2     │ 📨 1.2k  │
      │ Total   │ Online  │ Offline  │ Msgs/h   │
      └─────────┴─────────┴──────────┴──────────┘
   └─ 24h timeline chart loads below

2. DETECTION (3-5 seconds)
   └─ "Offline: 2" card is RED
   └─ User clicks card → navigates to Instances filtered by "offline"

3. ACTION (5-10 seconds)
   └─ Sees 2 red InstanceCards
   └─ Card shows: "Farmácia João • 📱 5511999... • 🔴 Offline 23min"
   └─ "Reconnect" button visible directly on card
   └─ Clicks → Badge changes to 🟡 pulsing "Reconnecting..."

4. CONFIRMATION (10-15 seconds)
   └─ Badge changes to 🟢 "Online"
   └─ Toast appears: "✅ Farmácia João reconnected"
   └─ Dashboard StatCard updates: Offline 2→1
```

### Success Criteria

| Criterion | Target |
|-----------|--------|
| First glance → know status | < 3 seconds |
| Identify which instance is down | < 5 seconds |
| Reconnect offline instance | < 10 seconds (2 clicks) |
| Create new instance end-to-end | < 60 seconds |
| Understand message graph | No legend explanation needed |

### Pattern Strategy

- **Established patterns** (no learning needed): Metric cards, sidebar, colored badges, toast notifications
- **Adapted patterns** (our twist): Clickable card as quick filter (click "Offline: 2" → filters instances), auto-reconnect with visual feedback

## Visual Design Foundation

### Typography Scale

| Element | Size | Weight | Usage |
|---------|------|--------|-------|
| `h1` | 24px | 600 | Page title ("Dashboard") |
| `h2` | 18px | 600 | Section title ("Instances") |
| `h3` | 16px | 500 | Card name ("Farmácia João") |
| `body` | 14px | 400 | General text, labels |
| `small` | 12px | 400 | Metadata, timestamps |
| `stat` | 32px | 700 | Large numbers in StatCards |

### Layout Grid

| Breakpoint | Columns | Sidebar | Usage |
|------------|---------|---------|-------|
| ≥ 1920px | 12 cols | 240px expanded | Full HD screen |
| ≥ 1440px | 12 cols | 240px expanded | Medium screen |
| ≥ 1024px | 8 cols | 60px collapsed | Smaller screen |
| < 1024px | 4 cols | Drawer overlay | Tablet (rare) |

### Accessibility Considerations

- All status colors meet WCAG AA contrast ratio on zinc-950 background
- Status is never communicated by color alone (always paired with text label or icon)
- All interactive elements have visible focus rings
- Font sizes never below 12px

## Design Directions — Page Layouts

### Dashboard Page

4 StatCards row → 24h Message Flow Chart → Recent Instances Grid.
StatCards are clickable: clicking "Offline: 2" navigates to Instances view filtered by offline status.

### Instances Page

SearchBar + filter tabs (All / Online / Offline / Warning) at top.
Grid of InstanceCards with status badge, phone number, instance name, and hover actions (Reconnect, Edit, Delete).
"+ New Instance" button opens a Sheet (side drawer) with a compact wizard: Name → Webhook → Proxy → QR Scan.

### Logs Page

DataTable with columns: Timestamp, Instance, Event Type, Details.
Filter by instance and event type. Text search across all fields.
Sortable columns, paginated results.

### Settings Page

Card-based sections: Default Webhook URL, Default Proxy URI, General Settings.
Inline save with toast confirmation. No full-page form submissions.

## User Journeys

### Journey 1: Morning Check-in (15s)

```
Open panel → Dashboard → 4 green StatCards → "All good" → close tab
```

### Journey 2: Instance Recovery (15s)

```
Dashboard → "Offline: 2" card (red) → Click → Instances filtered → Reconnect → Toast ✅
```

### Journey 3: New Instance Onboarding (60s)

```
Instances → "+ New" → Side Sheet → Name + Webhook + Proxy → Submit → QR Code appears → Scan → Online
```

### Journey 4: Anomaly Investigation (30s)

```
Dashboard → Graph shows drop → Click point → Instances → Find stopped one → Logs filtered by instance → Discover error
```

## Component Strategy

### Implementation Priority

| Priority | Component | Critical For |
|----------|-----------|-------------|
| 🔴 P0 | `Sidebar` + `Router` | Navigation to work |
| 🔴 P0 | `StatCard` | Dashboard to exist |
| 🔴 P0 | `InstanceCard` + `Badge` | Instance list |
| 🔴 P0 | `QRCodeDialog` | Device pairing |
| 🟡 P1 | `LineChart` (Recharts) | Message monitoring |
| 🟡 P1 | `Toast` (Sonner) | Action feedback |
| 🟡 P1 | `Sheet` (drawer) | Create/edit instance |
| 🟢 P2 | `DataTable` | Logs page |
| 🟢 P2 | `SearchBar` + `Filters` | Instance search/filter |
| 🟢 P2 | `SettingsForm` | Global configuration |

**Implementation order:** P0 → P1 → P2 (Functional skeleton → Features → Polish)

## UX Patterns

| Pattern | Implementation |
|---------|---------------|
| **Loading** | Skeleton shimmer on cards while loading (never white spinner) |
| **Empty state** | "No instances yet" with "+ Create your first instance" button |
| **Error state** | Red toast with retry button for API failures |
| **Optimistic update** | Badge changes to 🟡 instantly on Reconnect click, before API response |
| **WebSocket reconnect** | If WS disconnects, subtle top banner "Reconnecting..." with auto-retry |
| **Debounced search** | SearchBar only queries after 300ms typing pause |
| **Confirm destructive** | Delete instance shows confirmation dialog with instance name |

## Responsive Design & Accessibility

### Responsive Strategy

| Requirement | Implementation |
|-------------|---------------|
| **Primary target** | Desktop 1440px+ |
| **Sidebar** | 240px expanded (≥ 1440px), 60px collapsed (≥ 1024px), drawer (< 1024px) |
| **StatCards** | 4 columns (≥ 1440px), 2 columns (≥ 1024px), 1 column (< 1024px) |
| **InstanceCards** | 3 columns (≥ 1440px), 2 columns (≥ 1024px), 1 column (< 1024px) |

### Accessibility Requirements

| Requirement | Implementation |
|-------------|---------------|
| **Contrast** | WCAG AA on all text over zinc-950 background |
| **Color + text** | Status always has text badge alongside color |
| **Focus visible** | Blue ring on all interactive elements |
| **Keyboard nav** | Logical tab order, Enter/Space for actions |
| **Screen reader** | `aria-label` on all card action buttons |
| **Font minimum** | Never below 12px |

---

*UX Design Specification complete. Ready for implementation.*
