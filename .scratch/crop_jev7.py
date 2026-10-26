from PIL import Image
img = Image.open(r"C:\Users\yzq\Downloads\jev\7.png")
img.crop((0, 90, 1674, 340)).save(r"D:\src_test_env\opskat\.scratch\jev7-top.png")
