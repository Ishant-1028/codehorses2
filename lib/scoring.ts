// Keep in sync with packages/scoring-rules/rules.go

export const TEAM_SIZE = 6;
export const SALARY_CAP = 100;
export const MAX_PER_COUNTRY = 2;
export const CAPTAIN_MULTIPLIER = 2;
export const VICE_MULTIPLIER = 1.5;
export const RULES_VERSION = 1;

/**
 * Salary mirrors the Go formula exactly:
 * salary = clamp(round((rating - 1400) / 100), 3, 40)
 */
export function salary(rating: number): number {
  const raw = Math.round((rating - 1400) / 100);
  if (raw < 3) return 3;
  if (raw > 40) return 40;
  return raw;
}

export interface LineupValidation {
  ok: boolean;
  error?: string;
}

/**
 * Validates a lineup against all constraints:
 * - Exactly TEAM_SIZE handles
 * - All handles are unique
 * - Captain and vice-captain are in the handles array
 * - Captain and vice-captain are different
 * - Total salary <= SALARY_CAP
 */
export function validateLineup(
  handles: string[],
  captain: string,
  viceCaptain: string,
  salaries: number[]
): LineupValidation {
  // Check team size
  if (handles.length !== TEAM_SIZE) {
    return { ok: false, error: `Team must have exactly ${TEAM_SIZE} handles` };
  }

  // Check salaries length
  if (salaries.length !== TEAM_SIZE) {
    return { ok: false, error: "Salaries array must match handles length" };
  }

  // Check unique handles
  const uniqueHandles = new Set(handles.map(h => h.toLowerCase().trim()));
  if (uniqueHandles.size !== TEAM_SIZE) {
    return { ok: false, error: "All handles must be unique" };
  }

  // Check captain in handles
  const normalizedHandles = handles.map(h => h.toLowerCase().trim());
  const normalizedCaptain = captain.toLowerCase().trim();
  const normalizedVice = viceCaptain.toLowerCase().trim();

  if (!normalizedHandles.includes(normalizedCaptain)) {
    return { ok: false, error: "Captain must be one of the selected handles" };
  }

  // Check vice-captain in handles
  if (!normalizedHandles.includes(normalizedVice)) {
    return { ok: false, error: "Vice-captain must be one of the selected handles" };
  }

  // Check captain != vice
  if (normalizedCaptain === normalizedVice) {
    return { ok: false, error: "Captain and vice-captain must be different" };
  }

  // Check salary cap
  const totalSalary = salaries.reduce((sum, s) => sum + s, 0);
  if (totalSalary > SALARY_CAP) {
    return { ok: false, error: `Total salary (${totalSalary}) exceeds cap (${SALARY_CAP})` };
  }

  return { ok: true };
}
