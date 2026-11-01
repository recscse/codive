# Digital Twin: Future Vision

Status: **not started, not promised.** Do not advertise anything here until it ships and works on real projects. It starts only after codive's core launch has real users.

## The idea

A live model of a software project. The structure is shown in 3D: servers, APIs, modules, functions and databases, opening level by level down to the code. The fourth dimension is time: run the real project, watch real requests and data move through it, then rewind.

People could use it to search a project, trace a feature, learn a new codebase, debug, and find what is broken. All motion must come from real runtime data, never from decoration.

## What it builds on

| Capability | Status | Approach |
|---|---|---|
| Hierarchy, call tracing, entry points | Mostly built for Go | Existing code graph |
| Types of data passed | Close for Go | Parameter and return types from `go/types` |
| 3D view with icons | Straightforward | Three.js or Sigma.js on the existing graph |
| Live flow from running apps | Main new work | OpenTelemetry traces matched to graph nodes by function, file and line |
| Rewind and replay | Medium | Store traces locally, add a timeline |
| "What's broken?" | Medium | Compare expected flow with what ran: errors, slow steps, unreached paths |
| Debugging in the view | Hard | Debug Adapter Protocol, Delve for Go |
| Simulating a new feature | Hardest | Show what a change touches; replay recorded traffic on a test copy |

## Build order

Each step must be useful on its own.

1. Static twin: cube overview, hierarchy, icons, search, data types.
2. Live demo: one request through a small Go app, from API to function to database, then failing and showing where.
3. Timeline and replay.
4. Expected versus actual flow.
5. Debugging, then change simulation.

## Rules and known limits

- **The cube is an overview, not the only view.** In 3D, things hide behind each other. Real work happens in clear 2D flows, with search on every screen.
- **Tracing has a cost.** Python and Java have automatic agents. Go usually needs code changes or Linux-only eBPF. Start with requests, database calls and service calls; trace individual functions only on demand.
- **Simulation cannot predict behaviour.** Never claim it does.
- **Runtime data is sensitive.** Stay local. Show types and sizes by default, and hide values unless someone turns them on.

## Possible enterprise uses

Onboarding, incident response, safer changes, mapping legacy systems and dead code, and auditing where sensitive data travels. Enterprise sales would also need security reviews, access control and support.

Datadog, Jaeger and similar tools show services and traces, and Sourcegraph-style tools understand code. The opening is joining the two, down to the function, running locally.
