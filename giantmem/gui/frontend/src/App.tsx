import { useEffect, useState } from "react";
import "./App.css";
import { FacetCounts } from "../wailsjs/go/main/App";

type Facets = {
  byType: Record<string, number>;
  byLifecycle: Record<string, number>;
  byStatus: Record<string, number>;
};

function App() {
  const [facets, setFacets] = useState<Facets | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    FacetCounts()
      .then((f) => setFacets(f as Facets))
      .catch((e) => setErr(String(e)));
  }, []);

  return (
    <div id="App" style={{ padding: 24, fontFamily: "ui-sans-serif, system-ui" }}>
      <h1>giantmem</h1>
      <p style={{ opacity: 0.7 }}>read-only memory browser (smoke)</p>

      {err && <pre style={{ color: "crimson" }}>error: {err}</pre>}

      {facets && (
        <>
          <h3>FacetCounts.byType</h3>
          <pre>{JSON.stringify(facets.byType, null, 2)}</pre>
          <h3>byLifecycle</h3>
          <pre>{JSON.stringify(facets.byLifecycle, null, 2)}</pre>
          <h3>byStatus</h3>
          <pre>{JSON.stringify(facets.byStatus, null, 2)}</pre>
        </>
      )}

      {!facets && !err && <p>loading FacetCounts...</p>}
    </div>
  );
}

export default App;
