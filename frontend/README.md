# WikiAltas UI

Feishu-docs-like React frontend for WikiAltas v2.

## Run

```bash
npm install
npm run dev
```

Dev server: `http://localhost:5173`  
API proxy: `/api` → `http://localhost:8080`

## Build

```bash
npm run build
```

## Routes

| Path | View |
|------|------|
| `/` | Home: recent works, stub count, active runs |
| `/w/:slug` | Work view (tree + outline + editor) |
| `/w/:slug/folder` | Folder doc list |
| `/w/:slug/folder/:docSlug` | Doc editor |
| `/runs` | Run list |
| `/settings` | LLM / library / cleanup settings |

## Layout

```
┌────────┬──────────┬────────────────────┬─────────────┐
│ Global │ Universe │ Outline │ Content  │ AI Panel    │
│ Rail   │ Tree     │         │ Editor   │ (collapsible│
│ 48px   │ 240px    │ 160px   │ flex     │  360px)     │
└────────┴──────────┴────────────────────┴─────────────┘
```

## Stack

- React 18 + TypeScript + Vite
- Semi UI (`@douyinfe/semi-ui`)
- React Router v6
- marked + DOMPurify for Markdown
