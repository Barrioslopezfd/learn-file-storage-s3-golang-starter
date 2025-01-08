package main

func ar(main, second, times int) (result bool) {
	offset := 0.05
	if times == 9 {
		return float64(main)/float64(second)*float64(16) > float64(times)-offset && float64(main)/float64(second)*float64(16) < float64(times)+offset
	}
	if times == 16 {
		return float64(main)/float64(second)*float64(9) > float64(times)-offset && float64(main)/float64(second)*float64(9) < float64(times)+offset
	}
	return false
}
