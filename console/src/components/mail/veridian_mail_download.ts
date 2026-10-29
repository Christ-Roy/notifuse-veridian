// Enregistrement d'un fichier lu par fetch (jeton Bearer) : un lien direct ne porterait pas l'en-tête Authorization.

export function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.rel = 'noopener'
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  a.remove()
  // laisser au navigateur le temps de démarrer l'enregistrement
  setTimeout(() => URL.revokeObjectURL(url), 2000)
}

/** Lit un Blob en URL data: (les images incorporées d'un mail passent par img-src data:). */
export function blobToDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error ?? new Error('lecture impossible'))
    reader.readAsDataURL(blob)
  })
}
