"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { validateLineup, TEAM_SIZE, SALARY_CAP, salary } from "@/lib/scoring";

interface CFUser {
  handle: string;
  rating: number;
  country?: string;
}

export default function LineupBuilder({
  contestId,
  entryFeeCents,
}: {
  contestId: string;
  entryFeeCents: number;
}) {
  const router = useRouter();

  const [handles, setHandles] = useState<string[]>(Array(TEAM_SIZE).fill(""));
  const [ratings, setRatings] = useState<number[]>(Array(TEAM_SIZE).fill(0));
  const [captainIndex, setCaptainIndex] = useState<number | null>(null);
  const [viceIndex, setViceIndex] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleChange = (index: number, value: string) => {
    const updated = [...handles];
    updated[index] = value;
    setHandles(updated);

    // Reset captain/vice if the handle was cleared
    if (!value.trim()) {
      if (captainIndex === index) setCaptainIndex(null);
      if (viceIndex === index) setViceIndex(null);
    }
  };

  const handleRatingChange = (index: number, value: string) => {
    const updated = [...ratings];
    updated[index] = parseInt(value) || 0;
    setRatings(updated);
  };

  const salaries = ratings.map(r => salary(r));
  const totalSalary = salaries.reduce((sum, s) => sum + s, 0);

  async function handleSubmit() {
    setError(null);

    const cleanHandles = handles.map(h => h.trim()).filter(h => h !== "");

    if (cleanHandles.length !== TEAM_SIZE) {
      setError(`All ${TEAM_SIZE} handles are required`);
      return;
    }

    if (captainIndex === null || viceIndex === null) {
      setError("Select both captain and vice-captain");
      return;
    }

    const captain = handles[captainIndex].trim();
    const vice = handles[viceIndex].trim();

    // Validate lineup
    const validation = validateLineup(cleanHandles, captain, vice, salaries);
    if (!validation.ok) {
      setError(validation.error || "Invalid lineup");
      return;
    }

    setLoading(true);

    try {
      const idempotencyKey = crypto.randomUUID();

      const res = await fetch("/api/lineup/create", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          contestId,
          handles: cleanHandles,
          captain,
          viceCaptain: vice,
          salaries,
          idempotencyKey,
        }),
      });

      const data = await res.json();

      if (!res.ok) {
        setError(data.error || "Failed to create lineup");
        return;
      }

      router.push("/dashboard");
    } catch (err) {
      setError("Request failed. Please try again.");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-2xl p-8 w-full max-w-2xl shadow-lg">
      <h2 className="text-2xl font-bold mb-6 text-center">Build Your Lineup</h2>

      <div className="mb-6 p-4 bg-gray-800 rounded-xl">
        <div className="flex justify-between text-sm">
          <span className="text-gray-400">Entry Fee:</span>
          <span className="font-semibold">{entryFeeCents} PLAY_COIN</span>
        </div>
        <div className="flex justify-between text-sm mt-2">
          <span className="text-gray-400">Salary Used:</span>
          <span className={`font-semibold ${totalSalary > SALARY_CAP ? "text-red-500" : "text-green-400"}`}>
            {totalSalary} / {SALARY_CAP}
          </span>
        </div>
      </div>

      {error && (
        <div className="mb-4 p-3 bg-red-900/30 border border-red-500 rounded-xl text-red-400 text-sm">
          {error}
        </div>
      )}

      <div className="space-y-3 mb-6">
        {handles.map((handle, i) => (
          <div key={i} className="flex gap-2 items-center">
            <input
              value={handle}
              onChange={(e) => handleChange(i, e.target.value)}
              placeholder={`Handle ${i + 1}`}
              className="flex-1 px-4 py-2 rounded-xl border bg-gray-800 border-gray-700 text-white focus:outline-none focus:ring-2 focus:ring-purple-500"
            />
            <input
              type="number"
              value={ratings[i] || ""}
              onChange={(e) => handleRatingChange(i, e.target.value)}
              placeholder="Rating"
              className="w-24 px-3 py-2 rounded-xl border bg-gray-800 border-gray-700 text-white focus:outline-none focus:ring-2 focus:ring-purple-500"
            />
            <div className="w-16 text-center text-sm text-gray-400">
              ${salaries[i]}
            </div>
            <div className="flex gap-1">
              <button
                type="button"
                onClick={() => setCaptainIndex(i)}
                disabled={!handle.trim()}
                className={`px-2 py-1 rounded text-xs font-bold ${
                  captainIndex === i
                    ? "bg-yellow-500 text-black"
                    : "bg-gray-700 text-gray-400 hover:bg-gray-600"
                } disabled:opacity-30`}
              >
                C
              </button>
              <button
                type="button"
                onClick={() => setViceIndex(i)}
                disabled={!handle.trim()}
                className={`px-2 py-1 rounded text-xs font-bold ${
                  viceIndex === i
                    ? "bg-blue-500 text-white"
                    : "bg-gray-700 text-gray-400 hover:bg-gray-600"
                } disabled:opacity-30`}
              >
                VC
              </button>
            </div>
          </div>
        ))}
      </div>

      <button
        type="button"
        onClick={handleSubmit}
        disabled={loading || totalSalary > SALARY_CAP}
        className="w-full bg-purple-600 hover:bg-purple-700 py-3 rounded-xl font-semibold disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {loading ? "Creating Lineup..." : "Join Contest"}
      </button>
    </div>
  );
}
