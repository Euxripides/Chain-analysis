// Engines append section rows and mutate their active method. A failed method
// rolls back those changes together, so no references to abandoned rows escape.
export function sectionCheckpoint(result, methods = []) {
  const sections = new Map(Object.entries(result.sections).map(([key, rows]) => [key, rows.length]));
  const saved = methods.map(method => [method, structuredClone(method)]);
  return {
    rollback() {
      for (const [key, rows] of Object.entries(result.sections)) {
        if (sections.has(key)) rows.length = sections.get(key);
        else delete result.sections[key];
      }
      for (const [method, value] of saved) {
        for (const key of Object.keys(method)) delete method[key];
        Object.assign(method, value);
      }
    },
  };
}
