# 🌐 Domain Registration, Hosting & Deployment Guide

This guide explains how to register a custom domain, host the `devctx` landing page and docs site for **$0/month**, and set up the one-liner install scripts (`curl ... | bash` and `irm ... | iex`).

---

## 1. Domain Registration Recommendations

For developer tools and AI context engines, developers trust concise, modern TLDs:

| Domain | Est. Cost / Year | Why It Works |
| :--- | :---: | :--- |
| **`recscse.github.io/devctx`** *(Recommended)* | ~$12/yr | The gold standard for developer tools (owned by Google registry, requires HTTPS by default). |
| **`devctx.tools`** | ~$10/yr | Extremely descriptive and high availability. |
| **`devctx.sh`** | ~$25/yr | Excellent for terminal / CLI utility branding. |
| **`devctx.io`** | ~$35/yr | Classic tech startup TLD. |
| **`devctx.ai`** | ~$65/yr | Premium AI positioning. |

### Where to Register:
- **Cloudflare Registrar** (recommended: zero markup, at-cost pricing, free DNS & DDoS protection).
- **Porkbun** or **Namecheap**.

---

## 2. Zero-Cost Hosting Architecture ($0 / month)

You do **not** need expensive cloud VPS or server infrastructure. The entire `site/` directory contains self-contained static HTML, CSS, JavaScript, and install shell scripts.

### Option A: Cloudflare Pages (Recommended - Fastest & 100% Free)
1. Push your repository to GitHub: `github.com/your-username/devctx`.
2. Log into [Cloudflare Dashboard](https://dash.cloudflare.com) ➔ **Workers & Pages** ➔ **Create Application** ➔ **Pages**.
3. Connect your GitHub repository.
4. Set **Build output directory**: `site`.
5. Click **Deploy**.
6. Under **Custom Domains**, add `recscse.github.io/devctx`. Cloudflare automatically provisions a free wildcard SSL certificate.

---

### Option B: Vercel (1-Click Deployment)
1. Log into [Vercel](https://vercel.com).
2. Click **Add New Project** ➔ Import your `devctx` GitHub repository.
3. Set **Root Directory**: `site`.
4. Click **Deploy**.
5. Add your custom domain under **Settings ➔ Domains**.

---

### Option C: GitHub Pages (Direct from Repo)
1. In your GitHub repository, go to **Settings ➔ Pages**.
2. Under **Build and deployment**:
   - Source: `Deploy from a branch`
   - Branch: `main` / Folder: `/site` (or `/docs`)
3. Under **Custom domain**, enter `recscse.github.io/devctx` and click **Save**.

---

## 3. How the One-Liner Install Commands Work

When you deploy the `site/` folder, both `site/install.sh` and `site/install.ps1` are served directly as static files:

1. **macOS & Linux**:
   ```bash
   curl -fsSL https://recscse.github.io/devctx/install.sh | bash && devctx setup
   ```
   Cloudflare/Vercel delivers `site/install.sh` with `Content-Type: text/plain`, piping directly into `bash`.

2. **Windows PowerShell**:
   ```powershell
   irm https://recscse.github.io/devctx/install.ps1 | iex; devctx setup
   ```
   PowerShell downloads `site/install.ps1` into memory and executes it immediately via `Invoke-Expression` (`iex`).

---

## 4. DNS Configuration Summary (For Cloudflare / Namecheap)

| Type | Name | Content / Target | Proxy Status |
| :--- | :--- | :--- | :---: |
| **CNAME** | `@` / `recscse.github.io/devctx` | `<your-pages-subdomain>.pages.dev` | Proxied (Orange Cloud) |
| **CNAME** | `www` | `recscse.github.io/devctx` | Proxied (Orange Cloud) |

Once configured, your site will be live worldwide on Cloudflare's global edge network in `< 50ms` latency!
