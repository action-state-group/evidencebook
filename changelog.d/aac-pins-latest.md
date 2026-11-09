### Changed: pin agent-action-capsule go v0.21.0; evidence-interop/2 is still refused (#436)

- The module pins `agent-action-capsule/go` v0.21.0, whose `evidencerequest.Profiles` also names `evidence-interop/2`. The book does not implement `/2`, so a request naming it reads as an unknown profile everywhere the book reads a request (answering, `RequestSent`, `CheckStaple`), and the signed refusal's detail still reads `profiles supported: evidence-interop/0, evidence-interop/1`; it is never answered as `/0`.
