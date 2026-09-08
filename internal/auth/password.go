package auth

import "golang.org/x/crypto/bcrypt"

// HashPassword 使用 bcrypt 对明文密码进行哈希。
//
// 返回的字符串可直接持久化存储。bcrypt 自带随机盐，
// 相同明文每次哈希结果不同。
func HashPassword(password string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashed), nil
}

// CheckPassword 校验明文密码与哈希值是否匹配。
//
// 匹配时返回 nil，不匹配时返回非 nil 错误。
func CheckPassword(hashed, plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain))
}
