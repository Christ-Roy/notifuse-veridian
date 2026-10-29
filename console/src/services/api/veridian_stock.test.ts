import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('./client', () => ({ api: { get: vi.fn() } }))

import { api } from './client'
import { stockOverviewService } from './veridian_stock'

describe('stockOverviewService', () => {
  beforeEach(() => vi.clearAllMocks())

  it('lit stock.overview avec le workspace', async () => {
    vi.mocked(api.get).mockResolvedValue({ classes: [] })
    await stockOverviewService.get('ws-1')
    expect(api.get).toHaveBeenCalledWith('/api/veridian/stock.overview?workspace_id=ws-1')
  })

  it('passe la couverture visée quand elle est donnée', async () => {
    vi.mocked(api.get).mockResolvedValue({ classes: [] })
    await stockOverviewService.get('ws-1', 5)
    expect(api.get).toHaveBeenCalledWith('/api/veridian/stock.overview?workspace_id=ws-1&days=5')
  })
})
