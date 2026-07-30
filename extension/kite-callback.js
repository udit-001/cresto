(function () {
  try {
    var params = new URLSearchParams(location.search);
    if (params.get("action") === "login" && params.get("status") === "success") {
      window.location.replace("http://localhost:7777/portfolio?toast=Kite+connected&variant=success");
    }
  } catch (e) {
    console.error("[Cresto] Kite callback redirect error:", e);
  }
})();
