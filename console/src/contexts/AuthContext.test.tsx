import { describe, it, expect, vi, beforeEach, afterEach } from "vitest"
import { render, screen, act } from "@testing-library/react"
import { AuthProvider, useAuth } from "./AuthContext"
import { ApiError } from "../services/api/client"

// E2 (audit UI 19/10/2026) : un 429, un 5xx ou une coupure reseau sur user.me
// effacait le jeton et deconnectait. Seul un 401 doit deconnecter ; le reste :
// on garde la session et on reessaie avec backoff.

const getCurrentUser = vi.fn()
vi.mock("../services/api/auth", () => ({
  authService: { getCurrentUser: (...a: unknown[]) => getCurrentUser(...a), logout: vi.fn() },
  isRootUser: vi.fn()
}))
vi.mock("../services/api/workspace", () => ({ workspaceService: {} }))
vi.mock("../router", () => ({ router: { navigate: vi.fn() } }))

const OK = { user: { id: "u1", email: "a@example.com" }, workspaces: [] }

function Probe() {
  const { user, loading } = useAuth()
  return <div data-testid="s">{loading ? "loading" : user ? "in:" + user.email : "out"}</div>
}

async function flush(ms = 0) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })
}

describe("AuthProvider checkAuth", () => {
  beforeEach(() => {
    vi.useFakeTimers()
    localStorage.clear()
    localStorage.setItem("auth_token", "tok")
    getCurrentUser.mockReset()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it("401 : efface le jeton et deconnecte", async () => {
    getCurrentUser.mockRejectedValue(new ApiError("Unauthorized", 401))
    render(<AuthProvider><Probe /></AuthProvider>)
    await flush()
    expect(screen.getByTestId("s").textContent).toBe("out")
    expect(localStorage.getItem("auth_token")).toBeNull()
  })

  it.each([429, 500, 502, 503])("%i : garde le jeton et reessaie jusqu au succes", async (status) => {
    getCurrentUser
      .mockRejectedValueOnce(new ApiError("busy", status))
      .mockRejectedValueOnce(new ApiError("busy", status))
      .mockResolvedValue(OK)
    render(<AuthProvider><Probe /></AuthProvider>)
    await flush()
    expect(localStorage.getItem("auth_token")).toBe("tok")
    expect(screen.getByTestId("s").textContent).toBe("loading")
    await flush(10_000)
    expect(getCurrentUser).toHaveBeenCalledTimes(3)
    expect(screen.getByTestId("s").textContent).toBe("in:a@example.com")
    expect(localStorage.getItem("auth_token")).toBe("tok")
  })

  it("coupure reseau (TypeError) : garde le jeton et reessaie", async () => {
    getCurrentUser.mockRejectedValueOnce(new TypeError("Failed to fetch")).mockResolvedValue(OK)
    render(<AuthProvider><Probe /></AuthProvider>)
    await flush()
    expect(localStorage.getItem("auth_token")).toBe("tok")
    await flush(10_000)
    expect(screen.getByTestId("s").textContent).toBe("in:a@example.com")
  })

  it("echecs transitoires sans fin : abandonne l attente mais garde le jeton", async () => {
    getCurrentUser.mockRejectedValue(new ApiError("busy", 429))
    render(<AuthProvider><Probe /></AuthProvider>)
    await flush(300_000)
    expect(screen.getByTestId("s").textContent).toBe("out")
    expect(localStorage.getItem("auth_token")).toBe("tok")
  })

  it("un 401 pendant les reessais deconnecte", async () => {
    getCurrentUser
      .mockRejectedValueOnce(new ApiError("busy", 429))
      .mockRejectedValue(new ApiError("Unauthorized", 401))
    render(<AuthProvider><Probe /></AuthProvider>)
    await flush(10_000)
    expect(screen.getByTestId("s").textContent).toBe("out")
    expect(localStorage.getItem("auth_token")).toBeNull()
  })
})
