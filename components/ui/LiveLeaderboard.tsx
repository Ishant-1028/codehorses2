"use client";

import { useState, useEffect, useRef } from "react";

interface LeaderboardEntry {
  rank: number;
  handle: string;
  score: number;
  lineup_id?: string;
}

export default function LiveLeaderboard({ cfContestId }: { cfContestId: number }) {
  const [entries, setEntries] = useState<LeaderboardEntry[]>([]);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fallbackIntervalRef = useRef<NodeJS.Timeout | null>(null);

  const gatewayHost =
    typeof window !== "undefined"
      ? process.env.NEXT_PUBLIC_GATEWAY_HOST || "localhost:8080"
      : "localhost:8080";

  useEffect(() => {
    connectWebSocket();

    return () => {
      if (wsRef.current) {
        wsRef.current.close();
      }
      if (fallbackIntervalRef.current) {
        clearInterval(fallbackIntervalRef.current);
      }
    };
  }, [cfContestId]);

  function connectWebSocket() {
    try {
      const ws = new WebSocket(`ws://${gatewayHost}/ws/contest/${cfContestId}`);
      wsRef.current = ws;

      ws.onopen = () => {
        setConnected(true);
        setError(null);
        console.log("WebSocket connected");
      };

      ws.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data);
          if (data.leaderboard && Array.isArray(data.leaderboard)) {
            setEntries(data.leaderboard);
          }
        } catch (err) {
          console.error("Failed to parse WebSocket message:", err);
        }
      };

      ws.onerror = (err) => {
        console.error("WebSocket error:", err);
        setError("WebSocket connection error");
      };

      ws.onclose = () => {
        setConnected(false);
        console.log("WebSocket closed, falling back to HTTP polling");
        startFallbackPolling();
      };
    } catch (err) {
      console.error("Failed to connect WebSocket:", err);
      setError("Failed to connect");
      startFallbackPolling();
    }
  }

  function startFallbackPolling() {
    if (fallbackIntervalRef.current) return;

    // Fetch immediately
    fetchLeaderboard();

    // Then poll every 15 seconds
    fallbackIntervalRef.current = setInterval(() => {
      fetchLeaderboard();
    }, 15000);
  }

  async function fetchLeaderboard() {
    try {
      const res = await fetch(`http://${gatewayHost}/leaderboard/${cfContestId}`);
      if (res.ok) {
        const data = await res.json();
        if (data.leaderboard && Array.isArray(data.leaderboard)) {
          setEntries(data.leaderboard);
        }
      }
    } catch (err) {
      console.error("HTTP fallback fetch failed:", err);
    }
  }

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-2xl p-6 shadow-lg">
      <div className="flex justify-between items-center mb-4">
        <h2 className="text-2xl font-bold">Live Leaderboard</h2>
        <div className="flex items-center gap-2">
          <div
            className={`w-3 h-3 rounded-full ${
              connected ? "bg-green-500" : "bg-red-500"
            }`}
          />
          <span className="text-sm text-gray-400">
            {connected ? "Live" : "Polling"}
          </span>
        </div>
      </div>

      {error && (
        <div className="mb-4 p-3 bg-yellow-900/30 border border-yellow-500 rounded-xl text-yellow-400 text-sm">
          {error}
        </div>
      )}

      {entries.length === 0 ? (
        <p className="text-gray-500 text-center py-8">No entries yet</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="border-b border-gray-700">
                <th className="text-left py-2 px-3 text-gray-400 text-sm">Rank</th>
                <th className="text-left py-2 px-3 text-gray-400 text-sm">Handle</th>
                <th className="text-right py-2 px-3 text-gray-400 text-sm">Score</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((entry, idx) => (
                <tr
                  key={idx}
                  className="border-b border-gray-800 hover:bg-gray-800/50"
                >
                  <td className="py-2 px-3 font-semibold text-purple-400">
                    {entry.rank}
                  </td>
                  <td className="py-2 px-3">{entry.handle}</td>
                  <td className="py-2 px-3 text-right font-mono">
                    {entry.score.toFixed(2)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
