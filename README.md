# Notty

An accessible-first, lightweight personal knowledge management system built with Go, SQLite (FTS5), and HTMX.

## 🧠 Philosophy
Notty was designed for speed, privacy, and **Universal Design**. It follows the "Map of Content" (MoC) philosophy, allowing users to organize notes linearly or through structured backlinks and tags without losing focus.

### ♿ Accessibility at the Core
As a project developed by a blind engineer using screen readers (Orca/NVDA), Notty prioritizes:
- Semantic HTML over complex JS frameworks.
- ARIA Live Regions for real-time search feedback.
- Full keyboard navigability and focus management.
- Zero-bloat UI for cognitive and screen-reader clarity.

## 🛠 Tech Stack
- **Language:** Go (Golang) - For high-performance, static binaries.
- **Database:** SQLite - Multi-tenant architecture (one DB per user) with FTS5 for global search.
- **Frontend:** HTMX + Go Templates - Modern SPA-like experience with zero heavy Javascript.
- **Infrastructure:** Docker (Multi-stage builds) for lightweight, portable deployments.

## 🚀 Key Features
- **FTS5 Global Search:** High-performance full-text search with content snippets and keyword highlighting.
- **Wiki-links & Backlinks:** Automated bidirectional linking between notes using `[[Title]]` syntax.
- **Automated Taxonomies:** Tag extraction for structured indexing and filtering.
- **Secure Sessions:** Custom session management with bcrypt-hashed credentials.

## 🤖 AI-Driven Orchestration
This project is an exercise in **AI-Driven Orchestration**. It demonstrates how a Senior Data Engineer can leverage LLMs (Claude 3.5/GPT-4o) to architect, refactor, and tune a production-ready system, focusing on high-level design decisions and rigorous testing rather than manual boilerplate.

## 📦 Quick Start (Docker)
1. Clone the repo.
2. Create a `.env` file based on `.env.example`.
3. Build and run: `docker-compose up -d`