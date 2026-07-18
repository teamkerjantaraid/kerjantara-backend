package payment

// CreatePaymentRequest adalah request body untuk membuat pembayaran baru.
// swagger:model CreatePaymentRequest
type CreatePaymentRequest struct {
	JobID string `json:"job_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// CreatePaymentResponse adalah response setelah pembayaran berhasil dibuat.
// swagger:model CreatePaymentResponse
type CreatePaymentResponse struct {
	PaymentID              string  `json:"payment_id"                example:"a1b2c3d4-e5f6-7890-abcd-ef1234567890"`
	SnapToken              *string `json:"snap_token"                example:"snap-token-abc123"`
	AgreedPrice            int64   `json:"agreed_price"              example:"500000"`
	PlatformFee            int64   `json:"platform_fee"              example:"10000"`
	NetToWorker            int64   `json:"net_to_worker"             example:"490000"`
	TotalChargedToEmployer int64   `json:"total_charged_to_employer" example:"500000"`
	MidtransOrderID        string  `json:"midtrans_order_id"         example:"order-550e8400-1234"`
	FeeNote                string  `json:"fee_note"                  example:"Biaya layanan Rp 10.000 (transaksi di bawah Rp 1.000.000)"`
}

// WebhookRequest adalah payload yang dikirim oleh server Midtrans ke endpoint webhook.
// swagger:model WebhookRequest
type WebhookRequest struct {
	OrderID           string `json:"order_id"            example:"order-550e8400-1234"`
	TransactionStatus string `json:"transaction_status"  example:"settlement"`
	FraudStatus       string `json:"fraud_status"        example:"accept"`
}
