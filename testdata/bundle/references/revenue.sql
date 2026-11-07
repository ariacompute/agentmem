-- Sanctioned revenue YTD query. Values are bound, never interpolated.
SELECT SUM(amount) AS revenue_ytd
FROM orders
WHERE EXTRACT(YEAR FROM placed_at) = :year
  AND status = 'booked'
