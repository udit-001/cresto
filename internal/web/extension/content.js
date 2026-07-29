// Content script: marks the page so Cresto's /greythr page can detect
// that the extension is installed. Runs on localhost:7777 at document_start.
(function () {
  try {
    document.documentElement.setAttribute("data-cresto-extension", "installed");
    const marker = document.createElement("div");
    marker.id = "cresto-extension-installed";
    marker.style.display = "none";
    if (document.documentElement) {
      document.documentElement.appendChild(marker);
    }
  } catch (e) {
    console.error("[Cresto] content script error:", e);
  }
})();
