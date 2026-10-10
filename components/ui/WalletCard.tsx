"use client";

import { useState, useEffect } from "react";

interface WalletBalance {
  deposit_cents: number;
  winning_cents: number;
  bonus_cents: number;
  total_cents: number;
}

export default function WalletCard({ userId }: { userId: string }) {
  const [balance, setBalance] = useState<WalletBalance | null>(null);
  const [loading, setLoading] = useState(true);
  const [depositing, setDepositing] = useState(false);
  const [depositAmount, setDepositAmount] = useState("");

  useEffect(() => {
    fetchBalance();
  }, [userId]);

  async function fetchBalance() {
    try {
      const res = await fetch(`/api/wallet/${userId}`, {
        credentials: "include",
      });
      const data = await res.json();
      if (res.ok) {
        setBalance(data);
      }
    } catch (err) {
      console.error("Failed to fetch balance:", err);
    } finally {
      setLoading(false);
    }
  }

  async function handleDeposit() {
    const cents = parseInt(depositAmount);
    if (!cents || cents <= 0) {
      alert("Enter a valid amount");
      return;
    }

    setDepositing(true);

    try {
      const idempotencyKey = crypto.randomUUID();

      const res = await fetch("/api/wallet/deposit", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          userId,
          cents,
          idempotencyKey,
        }),
      });

      const data = await res.json();

      if (res.ok) {
        alert(data.deduped ? "Deposit already processed" : "Deposit successful!");
        setDepositAmount("");
        fetchBalance(); // Refresh balance
      } else {
        alert(data.error || "Deposit failed");
      }
    } catch (err) {
      alert("Request failed");
    } finally {
      setDepositing(false);
    }
  }

  if (loading) {
    return (
      <div className="bg-gray-900 border border-gray-800 rounded-2xl p-6 shadow-lg">
        <p className="text-gray-400">Loading wallet...</p>
      </div>
    );
  }

  return (
    <div className="bg-gray-900 border border-gray-800 rounded-2xl p-6 shadow-lg">
      <h2 className="text-2xl font-bold mb-6">Wallet</h2>

      <div className="space-y-4 mb-6">
        <div className="flex justify-between p-4 bg-gray-800 rounded-xl">
          <span className="text-gray-400">Deposit Balance</span>
          <span className="font-semibold text-green-400">
            {balance?.deposit_cents || 0} PLAY_COIN
          </span>
        </div>

        <div className="flex justify-between p-4 bg-gray-800 rounded-xl">
          <span className="text-gray-400">Winning Balance</span>
          <span className="font-semibold text-blue-400">
            {balance?.winning_cents || 0} PLAY_COIN
          </span>
        </div>

        <div className="flex justify-between p-4 bg-gray-800 rounded-xl">
          <span className="text-gray-400">Bonus Balance</span>
          <span className="font-semibold text-purple-400">
            {balance?.bonus_cents || 0} PLAY_COIN
          </span>
        </div>

        <div className="flex justify-between p-4 bg-purple-900/30 border border-purple-500 rounded-xl">
          <span className="text-gray-200 font-semibold">Total Balance</span>
          <span className="font-bold text-xl text-purple-300">
            {balance?.total_cents || 0} PLAY_COIN
          </span>
        </div>
      </div>

      <div className="border-t border-gray-700 pt-6">
        <h3 className="text-lg font-semibold mb-4">Add Funds (Play Money)</h3>
        <div className="flex gap-2">
          <input
            type="number"
            value={depositAmount}
            onChange={(e) => setDepositAmount(e.target.value)}
            placeholder="Amount (cents)"
            className="flex-1 px-4 py-2 rounded-xl border bg-gray-800 border-gray-700 text-white focus:outline-none focus:ring-2 focus:ring-purple-500"
          />
          <button
            onClick={handleDeposit}
            disabled={depositing}
            className="px-6 py-2 bg-purple-600 hover:bg-purple-700 rounded-xl font-semibold disabled:opacity-50"
          >
            {depositing ? "Processing..." : "Deposit"}
          </button>
        </div>
        <p className="text-xs text-gray-500 mt-2">
          This is play money for testing. No real money is involved.
        </p>
      </div>
    </div>
  );
}
